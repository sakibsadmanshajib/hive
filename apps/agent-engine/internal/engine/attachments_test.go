package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Issue #1065: a file attached in the composer before a Cowork run is started
// has to exist inside the sandbox, not merely have a row somewhere.
//
// Asserting that the launch payload carried the attachment would prove
// nothing, and that is exactly the shape of defect this repository keeps
// producing: the wiring exists and the value never arrives. So these read the
// bytes back out of the directory this launch bind mounts as /workspace, the
// same way the pack materialization tests do.

const attachmentBody = "hive-attachment-fixture-body: QAFILE7731\n"

func TestSandboxEngine_Launch_WritesAttachmentsIntoTheAgentWorkingDir(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngine(t, &fake)
	task := testTask()
	task.Attachments = []Attachment{{Name: "inventory.txt", Content: attachmentBody}}

	if _, err := e.Launch(context.Background(), task); err != nil {
		t.Fatalf("Launch: %v", err)
	}

	workingDir := filepath.Join(e.cfg.WorkspaceRoot, task.ID.String())
	got, err := os.ReadFile(filepath.Join(workingDir, "inventory.txt"))
	if err != nil {
		t.Fatalf("the attachment never reached the agent working directory: %v", err)
	}
	if string(got) != attachmentBody {
		t.Fatalf("attachment content = %q, want %q", got, attachmentBody)
	}
}

// The Working folder panel is the acceptance criterion's second half. The
// listing hides pack scaffolding on purpose (issue #1360), so an attachment
// landing beside the pack has to be checked rather than assumed.
func TestSandboxEngine_Launch_AttachmentsAppearInTheWorkingFolderListing(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngine(t, &fake)
	task := testTask()
	task.Attachments = []Attachment{{Name: "inventory.txt", Content: attachmentBody}}

	sessionRef, err := e.Launch(context.Background(), task)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	files, err := e.Files(context.Background(), sessionRef)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	for _, f := range files {
		if f.Name == "inventory.txt" {
			if f.Size != int64(len(attachmentBody)) {
				t.Fatalf("listing reports size %d for the attachment, want %d", f.Size, len(attachmentBody))
			}
			return
		}
	}
	t.Fatalf("the attachment is not in the working folder listing; got %+v", files)
}

// An attachment is named by the person who uploaded it, and the pack is
// planted in the same directory first. A file called AGENTS.md would otherwise
// replace the pack's own instructions with user supplied text, which is both a
// broken pack and a prompt injection with a very short path. It is kept, under
// a name that is free.
func TestSandboxEngine_Launch_AttachmentNeverReplacesAPackFile(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngine(t, &fake)
	task := testTask()
	task.Attachments = []Attachment{{Name: "AGENTS.md", Content: attachmentBody}}

	if _, err := e.Launch(context.Background(), task); err != nil {
		t.Fatalf("Launch: %v", err)
	}

	workingDir := filepath.Join(e.cfg.WorkspaceRoot, task.ID.String())
	pack, err := os.ReadFile(filepath.Join(workingDir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if string(pack) != packFixtureAgentsMD {
		t.Fatalf("the attachment overwrote the pack's AGENTS.md: %q", pack)
	}

	entries, err := os.ReadDir(workingDir)
	if err != nil {
		t.Fatalf("read working dir: %v", err)
	}
	for _, entry := range entries {
		body, readErr := os.ReadFile(filepath.Join(workingDir, entry.Name()))
		if readErr == nil && string(body) == attachmentBody {
			return
		}
	}
	t.Fatal("the colliding attachment was dropped instead of being kept under a free name")
}

// Task.Attachments arrives over the launcher socket from control-plane, which
// got it from a browser. This process is the one that turns a name into a
// path, so it validates the name itself rather than trusting the two hops
// above it, exactly as it already does for Task.Pack.
func TestSandboxEngine_Launch_RejectsAttachmentNamesThatAreNotFileNames(t *testing.T) {
	for _, name := range []string{
		"", "   ", ".", "..", "../escape.txt", "nested/file.txt", "back\\slash.txt",
		// A newline is not merely an odd file name. The name is repeated back
		// to the model as a bullet in the run's initial message, so one would
		// let the person forge extra lines in it.
		"a\x00b", "a\nb.txt", "a\tb.txt",
	} {
		t.Run(name, func(t *testing.T) {
			var fake *fakeAgentServer
			e := newTestEngine(t, &fake)
			task := testTask()
			task.Attachments = []Attachment{{Name: name, Content: attachmentBody}}

			if _, err := e.Launch(context.Background(), task); err == nil {
				t.Fatalf("Launch accepted attachment name %q", name)
			}
			if _, err := os.Stat(filepath.Join(e.cfg.WorkspaceRoot, task.ID.String())); !os.IsNotExist(err) {
				t.Fatalf("refused launch left its working directory behind: %v", err)
			}
		})
	}
}

// A file the agent is never told about is a file it has no reason to open.
// The names go on the initial message; the content does not, because the
// content is already on disk and the point of this path is that it does not
// have to fit in a prompt.
func TestSandboxEngine_Launch_TellsTheAgentWhichFilesAreAttached(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngine(t, &fake)
	task := testTask()
	task.Instructions = "Summarise the attached inventory."
	task.Attachments = []Attachment{{Name: "inventory.txt", Content: attachmentBody}}

	if _, err := e.Launch(context.Background(), task); err != nil {
		t.Fatalf("Launch: %v", err)
	}

	req := fake.startConversationRequest()
	if req.InitialMessage == nil || len(req.InitialMessage.Content) != 1 {
		t.Fatalf("expected one initial_message part, got %+v", req.InitialMessage)
	}
	text := req.InitialMessage.Content[0].Text
	if !strings.Contains(text, task.Instructions) {
		t.Fatalf("initial message lost the instructions: %q", text)
	}
	if !strings.Contains(text, "inventory.txt") {
		t.Fatalf("initial message never names the attached file: %q", text)
	}
	if strings.Contains(text, attachmentBody) {
		t.Fatalf("the attachment's content was stuffed into the prompt: %q", text)
	}
}

// Without attachments the message is the instructions and nothing else. The
// note is not a header that always renders.
func TestSandboxEngine_Launch_LeavesTheInitialMessageAloneWithoutAttachments(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngine(t, &fake)
	task := testTask()
	task.Instructions = "Summarise the attached inventory."

	if _, err := e.Launch(context.Background(), task); err != nil {
		t.Fatalf("Launch: %v", err)
	}

	req := fake.startConversationRequest()
	if req.InitialMessage == nil || req.InitialMessage.Content[0].Text != task.Instructions {
		t.Fatalf("initial message = %+v, want exactly the instructions", req.InitialMessage)
	}
}

// The working directory's name is the one place a task id becomes a path, and
// the id arrives as JSON over the launcher socket. uuid.UUID cannot hold a
// separator, so this is a guard against a future formatter change rather than
// a reachable input, which is exactly why it needs a test: nothing else would
// notice if it were deleted.
func TestWorkspaceDirName(t *testing.T) {
	id := uuid.New()
	name, err := workspaceDirName(id)
	if err != nil {
		t.Fatalf("workspaceDirName(%s): %v", id, err)
	}
	if name != id.String() {
		t.Fatalf("workspaceDirName = %q, want %q", name, id)
	}
	if _, err := workspaceDirName(uuid.Nil); err != nil {
		t.Fatalf("the nil UUID is still a legal directory name: %v", err)
	}
}
