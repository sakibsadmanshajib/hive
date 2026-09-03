/*
 * The agent API, served as a real HTTP stream, in front of the real chat
 * front end (issue #1622).
 *
 * WHY A PROXY RATHER THAN BROWSER INTERCEPTION
 *
 * The sibling capture for PR #1709 (docs/proof/cowork-step-streaming-1622)
 * intercepts the agent API with Playwright's `page.route` and answers each
 * call with `route.fulfill`. That cannot serve this change: `fulfill` sends a
 * complete body, and the whole claim here is that the body arrives in pieces
 * over an open connection. A capture that used it would be measuring a
 * response the harness had already finished writing, which is the thing this
 * change stops doing.
 *
 * So the browser talks to this process instead. Everything except
 * /api/v1/hive/agent/* is piped straight through to the real chat container,
 * websocket upgrades included, so the page under capture is the built front
 * end with nothing swapped out. The agent routes are answered here, and the
 * event stream is written frame by frame at the times the run produces them,
 * with a flush per frame, over a genuine chunked HTTP response.
 *
 * WHAT IS REAL AND WHAT IS SCRIPTED
 *
 * Real: the built front end, the composer, the submit path, agentTasks.ts's
 * fetch, its SSE parser, foldRunSteps, applyCoworkRun, the transcript
 * components, and the HTTP stream itself, which is a real chunked response the
 * browser reads incrementally.
 *
 * Scripted: what the sandbox did. There is no Apptainer on this machine (the
 * SIF is linux/amd64 and cannot be built or launched on WSL2), so the run is
 * played back. The frame vocabulary is not invented: it is exactly what
 * apps/control-plane/internal/agenttask/stream.go writes, pinned by
 * TestHandler_EventStream_DeliversAStepWrittenWhileTheClientIsListening and
 * its neighbours.
 *
 * MODE=stream serves the stream route from this process. MODE=poll answers it
 * 404, which is what a deployment without this change does, and sends the
 * front end down its fallback: the same steps, read on the three second timer.
 * That is the control, and the difference between the two captures is the
 * whole claim.
 *
 * MODE=live is the third, and it is the one that answers the review finding
 * the first two could not have caught. It proxies the stream to a real Go
 * listener built by httpserver.New, the same constructor cmd/server calls,
 * with its fifteen second WriteTimeout unchanged
 * (apps/control-plane/cmd/streamproofserver). Frames rendered under it have
 * crossed the socket that used to cut every stream at fifteen seconds. Neither
 * of the other two modes could show that, because Node has no equivalent of
 * Go's whole-response write deadline, so a capture against this file alone was
 * measuring a harness that could not reproduce the defect.
 */

import http from 'node:http';
import net from 'node:net';

const PORT = Number(process.env.PORT || 3423);
const UPSTREAM = new URL(process.env.OWUI_URL || 'http://127.0.0.1:3422');
const MODE = process.env.MODE || 'stream';
// MODE=live only: the real control-plane stream this proxies to. No credential
// is set, because streamproofserver mounts the internal mux directly rather
// than behind RequireInternalToken; it holds no data and listens on loopback.
const LIVE_STREAM_URL = process.env.LIVE_STREAM_URL || '';

const TASK_ID = '6567c0fb-e34a-4609-9fc7-bbea32fde598';
const INSTRUCTIONS =
	'Create a file named sixcap.txt containing the text HIVE-COWORK-OK, then show its contents';

/*
 * The run, and when each step happens.
 *
 * Deliberately faster than the sibling capture's, because the point of this
 * one is the gap between a step happening and a step appearing. Steps roughly
 * a second apart are the cadence a real coding run produces and the cadence a
 * three second poll cannot render honestly: two of these land inside one poll
 * interval and arrive as a lump.
 */
const SANDBOX_READY_MS = 2000;
const STEPS = [
	{ at: 3000, seq: 1, source_event_id: 'e1', kind: 'tool_call', payload: { tool_name: 'bash', tool_call_id: 'c1', preview: 'list the workspace' } },
	{ at: 4200, seq: 2, source_event_id: 'e2', kind: 'tool_result', payload: { tool_name: 'bash', tool_call_id: 'c1', preview: 'AGENTS.md' } },
	{ at: 5400, seq: 3, source_event_id: 'e3', kind: 'tool_call', payload: { tool_name: 'str_replace_editor', tool_call_id: 'c2', preview: 'write sixcap.txt' } },
	{ at: 6600, seq: 4, source_event_id: 'e4', kind: 'tool_result', payload: { tool_name: 'str_replace_editor', tool_call_id: 'c2', preview: 'wrote 14 bytes' } },
	{ at: 7800, seq: 5, source_event_id: 'file:sixcap.txt:14:1756800000', kind: 'file', payload: { name: 'sixcap.txt', size: 14 } },
	{ at: 9000, seq: 6, source_event_id: 'e6', kind: 'tool_call', payload: { tool_name: 'bash', tool_call_id: 'c3', preview: 'cat sixcap.txt' } },
	{ at: 10200, seq: 7, source_event_id: 'e7', kind: 'tool_result', payload: { tool_name: 'bash', tool_call_id: 'c3', preview: 'HIVE-COWORK-OK' } }
];
const FINISHED_MS = 11500;
const SUMMARY = 'sixcap.txt now holds HIVE-COWORK-OK';

let submittedAt = null;
const elapsed = () => (submittedAt === null ? 0 : Date.now() - submittedAt);

/** Every log line the capture commits, so the timings are checkable. */
export const wireLog = [];
const say = (line) => {
	const stamp = (elapsed() / 1000).toFixed(2).padStart(6);
	wireLog.push(`[run +${stamp}s] ${line}`);
	console.log(`[wire] [run +${stamp}s] ${line}`);
};

const taskAt = (ms) => ({
	id: TASK_ID,
	pack: 'knowledge-work-pack',
	instructions: INSTRUCTIONS,
	status: ms >= FINISHED_MS ? 'succeeded' : ms >= SANDBOX_READY_MS ? 'running' : 'queued',
	engine_session_ref: ms >= SANDBOX_READY_MS ? 'session-1' : '',
	result_summary_ref: ms >= FINISHED_MS ? SUMMARY : '',
	error_message: '',
	created_at: new Date(Date.now() - ms).toISOString(),
	updated_at: new Date().toISOString(),
	started_at: ms >= SANDBOX_READY_MS ? new Date(Date.now() - ms + SANDBOX_READY_MS).toISOString() : null,
	finished_at: ms >= FINISHED_MS ? new Date().toISOString() : null
});

const eventsAt = (ms, afterSeq) =>
	STEPS.filter((s) => ms >= s.at && s.seq > afterSeq).map((s) => ({
		seq: s.seq,
		source_event_id: s.source_event_id,
		kind: s.kind,
		payload: s.payload,
		created_at: new Date().toISOString()
	}));

const sendJSON = (res, body, status = 200) => {
	const text = JSON.stringify(body);
	res.writeHead(status, {
		'Content-Type': 'application/json',
		'Content-Length': Buffer.byteLength(text)
	});
	res.end(text);
};

/**
 * The subscription, written frame by frame as the run produces them.
 *
 * Mirrors handleEventStream in apps/control-plane/internal/agenttask/stream.go
 * exactly where it matters: the status frame first, then the steps, then one
 * end frame, and the pass that observes the terminal status still drains the
 * steps before it sends that end frame. A harness that ended first would be
 * playing back the bug rather than the fix.
 */
const serveStream = (req, res) => {
	const url = new URL(req.url, 'http://localhost');
	let cursor = Number(url.searchParams.get('after_seq') || 0);
	res.writeHead(200, {
		'Content-Type': 'text/event-stream',
		'Cache-Control': 'no-cache',
		Connection: 'keep-alive',
		'X-Accel-Buffering': 'no'
	});
	say(`stream opened at after_seq=${cursor}`);

	let status = null;
	const frame = (event, payload) => {
		res.write(`event: ${event}\ndata: ${JSON.stringify(payload)}\n\n`);
	};

	const tick = setInterval(() => {
		const ms = elapsed();
		const task = taskAt(ms);
		if (task.status !== status) {
			status = task.status;
			frame('status', task);
			say(`frame status=${status}`);
		}
		for (const event of eventsAt(ms, cursor)) {
			frame('step', event);
			cursor = Math.max(cursor, event.seq);
			say(`frame step seq=${event.seq} ${event.kind} ${event.payload.preview ?? event.payload.name ?? ''}`);
		}
		if (task.status === 'succeeded' || task.status === 'failed' || task.status === 'cancelled') {
			frame('end', { status: task.status });
			say(`frame end status=${task.status}`);
			clearInterval(tick);
			res.end();
		}
	}, 500);

	req.on('close', () => {
		clearInterval(tick);
	});
};

/**
 * MODE=live: pipe the real control-plane stream through, byte for byte.
 *
 * Nothing here parses or reshapes a frame. The point of this mode is that what
 * the browser renders is exactly what a Go listener under a real WriteTimeout
 * wrote, so anything this process rewrote on the way would be the stand-in it
 * exists to remove.
 */
const relayLiveStream = (req, res, url) => {
	if (!LIVE_STREAM_URL) {
		res.writeHead(500, { 'Content-Type': 'application/json' });
		return res.end(JSON.stringify({ error: { message: 'LIVE_STREAM_URL is not set' } }));
	}
	const cursor = url.searchParams.get('after_seq') || '0';
	const target = new URL(`${LIVE_STREAM_URL}?after_seq=${encodeURIComponent(cursor)}`);
	say(`live stream opened at after_seq=${cursor} against ${target.origin}`);

	const upstream = http.request(
		{
			host: target.hostname,
			port: target.port,
			method: 'GET',
			path: target.pathname + target.search,
			headers: { Accept: 'text/event-stream' }
		},
		(upstreamRes) => {
			res.writeHead(upstreamRes.statusCode ?? 502, {
				'Content-Type': upstreamRes.headers['content-type'] || 'text/event-stream',
				'Cache-Control': 'no-cache',
				'X-Accel-Buffering': 'no'
			});
			let frames = 0;
			const opened = Date.now();
			upstreamRes.on('data', (chunk) => {
				const text = chunk.toString();
				for (const line of text.split('\n')) {
					if (line.startsWith('event: ')) {
						frames += 1;
						say(`live frame ${frames}: ${line.slice(7)} (+${((Date.now() - opened) / 1000).toFixed(1)}s into the connection)`);
					}
				}
				res.write(chunk);
				res.flushHeaders?.();
			});
			upstreamRes.on('end', () => {
				say(`live stream ended after ${((Date.now() - opened) / 1000).toFixed(1)}s carrying ${frames} frame(s)`);
				res.end();
			});
			upstreamRes.on('error', (err) => {
				// The failure this mode exists to be able to observe. A cut
				// mid-response is what a WriteTimeout looks like from here.
				say(`live stream ERRORED after ${((Date.now() - opened) / 1000).toFixed(1)}s: ${err.message}`);
				res.end();
			});
		}
	);
	upstream.on('error', (err) => {
		say(`live stream could not be opened: ${err.message}`);
		res.writeHead(502).end();
	});
	req.on('close', () => upstream.destroy());
	upstream.end();
};

const serveAgent = (req, res) => {
	const url = new URL(req.url, 'http://localhost');
	const path = url.pathname;

	if (path.endsWith('/__elapsed')) {
		/*
		 * How far into the run this process thinks it is.
		 *
		 * Not part of the product's API; the capture reads it to base its own
		 * measurements on the moment the submission actually landed here
		 * rather than on the moment it pressed Enter. On a cold container
		 * those are six seconds apart, and the first capture taken this way
		 * reported every step as uniformly six seconds late, which is a
		 * measurement of the harness rather than of the product.
		 */
		req.resume();
		return sendJSON(res, { submitted: submittedAt !== null, elapsed_ms: elapsed() });
	}
	if (req.method === 'POST' && path.endsWith('/tasks')) {
		submittedAt = Date.now();
		say('composer submitted the run');
		req.resume();
		return sendJSON(res, taskAt(0));
	}
	if (path.endsWith('/events/stream')) {
		if (MODE === 'live') {
			return relayLiveStream(req, res, url);
		}
		if (MODE !== 'stream') {
			// The control. A deployment without this change has no such route,
			// and the front end has to notice and fall back rather than
			// stranding the run.
			say('stream route refused (control run)');
			return sendJSON(res, { error: { message: 'not found' } }, 404);
		}
		return serveStream(req, res);
	}
	if (path.endsWith('/events')) {
		const afterSeq = Number(url.searchParams.get('after_seq') || 0);
		const events = eventsAt(elapsed(), afterSeq);
		say(`cursor read after_seq=${afterSeq} -> ${events.length} step(s)`);
		return sendJSON(res, { events });
	}
	if (req.method === 'GET') {
		const task = taskAt(elapsed());
		say(`task read -> ${task.status}`);
		return sendJSON(res, task);
	}
	req.resume();
	return sendJSON(res, {});
};

/** Everything that is not the agent API goes to the real chat container. */
const passThrough = (req, res) => {
	const upstream = http.request(
		{
			host: UPSTREAM.hostname,
			port: UPSTREAM.port,
			method: req.method,
			path: req.url,
			headers: { ...req.headers, host: UPSTREAM.host }
		},
		(upstreamRes) => {
			res.writeHead(upstreamRes.statusCode ?? 502, upstreamRes.headers);
			upstreamRes.pipe(res);
		}
	);
	upstream.on('error', () => {
		res.writeHead(502).end();
	});
	req.pipe(upstream);
};

const server = http.createServer((req, res) => {
	if (req.url.startsWith('/api/v1/hive/agent/')) {
		return serveAgent(req, res);
	}
	return passThrough(req, res);
});

// Open WebUI runs a websocket for its own realtime updates. Dropping the
// upgrade would leave the page in a degraded state that has nothing to do with
// this change and everything to do with the harness.
server.on('upgrade', (req, socket, head) => {
	const upstream = http.request({
		host: UPSTREAM.hostname,
		port: UPSTREAM.port,
		method: req.method,
		path: req.url,
		headers: { ...req.headers, host: UPSTREAM.host }
	});
	upstream.on('upgrade', (upstreamRes, upstreamSocket, upstreamHead) => {
		socket.write(
			'HTTP/1.1 101 Switching Protocols\r\n' +
				Object.entries(upstreamRes.headers)
					.map(([k, v]) => `${k}: ${v}\r\n`)
					.join('') +
				'\r\n'
		);
		if (upstreamHead && upstreamHead.length) socket.write(upstreamHead);
		upstreamSocket.pipe(socket);
		socket.pipe(upstreamSocket);
	});
	upstream.on('error', () => socket.destroy());
	if (head && head.length) upstream.write(head);
	upstream.end();
});

server.listen(PORT, '127.0.0.1', () => {
	console.log(`[wire] mode=${MODE} listening on ${PORT}, upstream ${UPSTREAM.origin}`);
});

// Keep the process's own socket errors from taking the capture down with a
// stack trace that says nothing about the run.
server.on('clientError', (_err, socket) => {
	if (socket instanceof net.Socket) socket.destroy();
});
