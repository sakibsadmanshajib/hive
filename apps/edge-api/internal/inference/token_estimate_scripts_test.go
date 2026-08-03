package inference

import (
	"strings"
	"testing"
)

// Issue #673: the unconfirmed-usage estimator is a customer-facing money
// figure, so it must never exceed real token usage, and the size of its error
// must not depend on the script the customer writes in. Bangladesh is the
// first market, so Bengali is the case that matters most.
//
// minRealTokens below is MEASURED, not assumed. Every figure is the smallest
// count returned by tiktoken 0.13.0 across o200k_base, cl100k_base and
// o200k_harmony for scriptRepeat copies of the sentence. The smallest is the
// binding reference: an estimate at or below it cannot overcharge on any of
// the three, and o200k_harmony is the tokenizer of the gpt-oss model the
// hive-fast alias pins to (D-032), so this is not a hypothetical family.
// Reproduce with:
//
//	pip install tiktoken==0.13.0
//	python -c 'import tiktoken; print(len(tiktoken.get_encoding("o200k_base").encode(SENTENCE * 60)))'
type scriptCase struct {
	name          string
	sentence      string // the same sentence in each script, semantically equivalent
	minRealTokens int64  // smallest real count across the three encodings
}

// scriptRepeat pushes every case into the thousands of characters. Small
// inputs prove nothing here: estimateCompletionTokens floors at 1 credit, and
// at CreditsPerUSD = 100_000 that floor hides any amount of under-pricing.
const scriptRepeat = 60

var scriptCases = []scriptCase{
	{
		name:     "latin_english",
		sentence: "Artificial intelligence is changing how businesses work in Bangladesh.",
		// o200k_base 601, cl100k_base 602, o200k_harmony 601
		minRealTokens: 601,
	},
	{
		name:     "bengali",
		sentence: "কৃত্রিম বুদ্ধিমত্তা বাংলাদেশে ব্যবসার কাজ করার ধরন পাল্টে দিচ্ছে।",
		// o200k_base 1441, cl100k_base 4561, o200k_harmony 1441
		minRealTokens: 1441,
	},
	{
		name:     "chinese",
		sentence: "人工智能正在改变孟加拉国的商业运作方式。",
		// o200k_base 900, cl100k_base 1260, o200k_harmony 900
		minRealTokens: 900,
	},
	{
		name:     "japanese",
		sentence: "人工知能はバングラデシュでのビジネスの進め方を変えつつある。",
		// o200k_base 1620, cl100k_base 1980, o200k_harmony 1620
		minRealTokens: 1620,
	},
	{
		name:     "korean",
		sentence: "인공지능은 방글라데시에서 기업이 일하는 방식을 바꾸고 있습니다.",
		// o200k_base 1261, cl100k_base 1741, o200k_harmony 1261
		minRealTokens: 1261,
	},
	{
		name:     "arabic",
		sentence: "الذكاء الاصطناعي يغير طريقة عمل الشركات في بنغلاديش.",
		// o200k_base 1081, cl100k_base 2281, o200k_harmony 1081
		minRealTokens: 1081,
	},
	{
		name:     "devanagari",
		sentence: "कृत्रिम बुद्धिमत्ता बांग्लादेश में व्यवसायों के काम करने का तरीका बदल रही है।",
		// o200k_base 1322, cl100k_base 4621, o200k_harmony 1322
		minRealTokens: 1322,
	},
	{
		name:     "emoji",
		sentence: "👨‍👩‍👧‍👦 🇧🇩 🙂 🎉 ✅ 🚀",
		// o200k_base 1261, cl100k_base 1922, o200k_harmony 1261
		minRealTokens: 1261,
	},
}

func repeated(c scriptCase) string { return strings.Repeat(c.sentence+" ", scriptRepeat) }

// TestEstimateCompletionTokens_NeverOverchargesAnyScript is the hard bound: on
// an unmeasured settlement the estimate must err LOW for every writing system,
// not just for Latin. The byte-per-four formula this replaced overcharged six
// of these eight scripts against o200k_base, Bengali at 1.87x real and
// Devanagari at 2.36x.
func TestEstimateCompletionTokens_NeverOverchargesAnyScript(t *testing.T) {
	for _, c := range scriptCases {
		t.Run(c.name, func(t *testing.T) {
			got := estimateCompletionTokens(repeated(c))
			t.Logf("estimate %d, real %d, %.3f of real usage", got, c.minRealTokens, float64(got)/float64(c.minRealTokens))
			if got > c.minRealTokens {
				t.Errorf("estimate %d exceeds real usage %d tokens (%.2fx): an unmeasured charge must favour the customer",
					got, c.minRealTokens, float64(got)/float64(c.minRealTokens))
			}
			// The other direction still has to hold: erring low must not mean
			// erring to nothing. Free inference on provider-controlled input is
			// the failure mode that took billing down for three days.
			if floor := c.minRealTokens * 15 / 100; got < floor {
				t.Errorf("estimate %d is below %d, 15%% of real usage %d: the estimator has collapsed toward free inference",
					got, floor, c.minRealTokens)
			}
		})
	}
}

// TestEstimateCompletionTokens_ChargeParityAcrossScripts is the fairness bound.
//
// It is stated on the FRACTION OF TRUE USAGE each script is charged, not on the
// raw charge, because real tokenizers are not script-neutral: measured on
// o200k_base the same sentence costs 601 tokens in English and 1441 in Bengali,
// so equal raw charges would require deliberately mispricing one of them by
// 2.4x. Equal fractions of real usage is the bound that means nobody is
// penalised for their writing system.
//
// Tolerance: Bengali within 10 percent of English, and no more than 2x spread
// across the three. Measured after the fix: English 59 percent, Bengali 62
// percent, Chinese 34 percent of real usage.
//
// This assertion is invariant to the divisor: both ratios are linear in it, so
// it tests the UNIT the estimator counts in, which is exactly what #673 is
// about. Counting runes instead of UTF-8 bytes fails it at 61 percent apart,
// because Bengali really does consume about 2.6x the tokens per character that
// English does, and byte length is the only cheap measure that tracks that.
func TestEstimateCompletionTokens_ChargeParityAcrossScripts(t *testing.T) {
	pct := func(name string) int64 {
		for _, c := range scriptCases {
			if c.name == name {
				return estimateCompletionTokens(repeated(c)) * 100 / c.minRealTokens
			}
		}
		t.Fatalf("no script case named %q", name)
		return 0
	}
	en, bn, zh := pct("latin_english"), pct("bengali"), pct("chinese")

	drift := bn - en
	if drift < 0 {
		drift = -drift
	}
	if drift*100 > en*10 {
		t.Errorf("Bengali is charged %d%% of its real usage against English %d%%, more than 10%% apart: the estimator penalises a writing system", bn, en)
	}
	lo, hi := en, en
	for _, v := range []int64{bn, zh} {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	if hi > lo*2 {
		t.Errorf("fraction-of-real-usage spread across English %d%%, Bengali %d%%, Chinese %d%% exceeds 2x", en, bn, zh)
	}
	for _, v := range []struct {
		name string
		p    int64
	}{{"english", en}, {"bengali", bn}, {"chinese", zh}} {
		if v.p > 100 {
			t.Errorf("%s is charged %d%% of real usage: over 100%% is an overcharge", v.name, v.p)
		}
	}
}

// TestEstimateCompletionTokens_WhitespaceRunsDoNotInflate covers #673's
// worst-case input. Byte-pair encoders carry multi-whitespace tokens, so a run
// of N whitespace characters costs far fewer than N/4 tokens: 40,000 spaces is
// 313 real tokens and the old formula charged 10,000 of them, the entire hold.
func TestEstimateCompletionTokens_WhitespaceRunsDoNotInflate(t *testing.T) {
	tests := []struct {
		name          string
		in            string
		minRealTokens int64
	}{
		{
			name: "forty thousand spaces",
			in:   strings.Repeat(" ", 40000),
			// 40000 bytes; o200k_base 313, cl100k_base 313, o200k_harmony 313
			minRealTokens: 313,
		},
		{
			name: "one thousand newlines",
			in:   strings.Repeat("\n", 1000),
			// 1000 bytes; o200k_base 63, cl100k_base 32, o200k_harmony 63
			minRealTokens: 32,
		},
		{
			name: "words padded with fifty spaces each",
			in:   strings.Repeat("word"+strings.Repeat(" ", 50), 200),
			// 10800 bytes; o200k_base 400, cl100k_base 400, o200k_harmony 400
			minRealTokens: 400,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := estimateCompletionTokens(tt.in); got > tt.minRealTokens {
				t.Errorf("estimate %d exceeds real usage %d tokens: a whitespace run is not tokens", got, tt.minRealTokens)
			}
		})
	}
}

// textCase is a measured fixture for the two run-collapse bounds below. Every
// minRealTokens is again the SMALLEST count across tiktoken 0.13.0 o200k_base,
// cl100k_base and o200k_harmony, and wantBytes guards the fixture itself: these
// figures were measured on the exact byte string, so a transcription slip in
// the builder below has to fail loudly rather than quietly re-baseline a money
// bound.
type textCase struct {
	name          string
	in            string
	wantBytes     int
	minRealTokens int64
}

func (tc textCase) check(t *testing.T) int64 {
	t.Helper()
	if len(tc.in) != tc.wantBytes {
		t.Fatalf("fixture is %d bytes, measured against %d: the measured token counts no longer describe this input", len(tc.in), tc.wantBytes)
	}
	return estimateCompletionTokens(tc.in)
}

// TestEstimateCompletionTokens_SeparatorRunsDoNotOvercharge is the other half of
// the bound the script test states. Byte length is NOT a structural upper bound
// on token count: byte-pair vocabularies carry long single tokens for repeated
// punctuation, so real bytes per token reaches 64 for a run of dashes and 128
// for a run of spaces, far above the divisor. Measured on this fixture set
// before the fix, an unmeasured settlement over-charged a horizontal rule by
// 6.59x, a log banner by 4.21x and a blank form by 0.93 of real, and every one
// of these shapes is output a model produces routinely.
//
// The hard bound is est <= real, same as the script test. The second bound is a
// margin: for the shapes that show up inside ordinary assistant output the
// estimate must keep at least 20 percent of headroom, because a fixture sitting
// at 0.98 of real is one vocabulary change away from over-charging.
func TestEstimateCompletionTokens_SeparatorRunsDoNotOvercharge(t *testing.T) {
	tests := []textCase{
		{
			name:      "horizontal rules of seventy eight dashes",
			in:        strings.Repeat(strings.Repeat("-", 78)+"\n", 200),
			wantBytes: 15800,
			// o200k_base 200, cl100k_base 400, o200k_harmony 200 (79.0 real bytes/token)
			minRealTokens: 200,
		},
		{
			name: "twenty thousand bytes of repeated separators",
			in: strings.Repeat("=", 5000) + strings.Repeat("-", 5000) +
				strings.Repeat("*", 5000) + strings.Repeat("/", 5000),
			wantBytes: 20000,
			// o200k_base 312, cl100k_base 314, o200k_harmony 312 (64.1 real bytes/token)
			minRealTokens: 312,
		},
		{
			name:      "log banner of a hundred equals signs per line",
			in:        strings.Repeat(strings.Repeat("=", 100)+"\n", 300),
			wantBytes: 30300,
			// o200k_base 600, cl100k_base 900, o200k_harmony 600
			minRealTokens: 600,
		},
		{
			name:      "blank form lines of underscore runs",
			in:        strings.Repeat("Name: "+strings.Repeat("_", 60)+"\n", 400),
			wantBytes: 26800,
			// o200k_base 2400, cl100k_base 2400, o200k_harmony 2400
			minRealTokens: 2400,
		},
		{
			name:      "table of contents leader dots",
			in:        strings.Repeat("Chapter heading "+strings.Repeat(".", 40)+" 12\n", 500),
			wantBytes: 30000,
			// o200k_base 3500, cl100k_base 3500, o200k_harmony 3500
			minRealTokens: 3500,
		},
		{
			name:      "markdown table separator rows",
			in:        strings.Repeat("|---|---|---|---|\n", 1400),
			wantBytes: 25200,
			// o200k_base 12600, cl100k_base 12600, o200k_harmony 12600
			minRealTokens: 12600,
		},
		{
			name:      "ascii box drawing table",
			in:        asciiBoxTable(120, 60),
			wantBytes: 29760,
			// o200k_base 1680, cl100k_base 1680, o200k_harmony 1680. The densest
			// realistic formatting measured: 0.83 of real usage before this fix,
			// and an independent measurement of a narrower variant reached 0.976.
			minRealTokens: 1680,
		},
		{
			name: "report with a horizontal rule per section",
			in: strings.Repeat(strings.Repeat("=", 78)+"\nSection: findings\n"+
				strings.Repeat("The gateway settled every reservation exactly once.\n", 3), 60),
			wantBytes: 15180,
			// o200k_base 1800, cl100k_base 1800, o200k_harmony 1800
			minRealTokens: 1800,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.check(t)
			t.Logf("%d bytes, estimate %d, real %d, %.3f of real usage", tt.wantBytes, got, tt.minRealTokens, float64(got)/float64(tt.minRealTokens))
			if got > tt.minRealTokens {
				t.Errorf("estimate %d exceeds real usage %d tokens (%.2fx): a separator run is not one token per twelve bytes",
					got, tt.minRealTokens, float64(got)/float64(tt.minRealTokens))
			}
			if got*100 > tt.minRealTokens*80 {
				t.Errorf("estimate %d is %d%% of real usage %d: formatted output must keep at least 20%% of headroom",
					got, got*100/tt.minRealTokens, tt.minRealTokens)
			}
		})
	}
}

// asciiBoxTable builds rows of a two-column ASCII box-drawing table: a border
// line of dashes, then a data line whose cells are padded with spaces.
func asciiBoxTable(rows, width int) string {
	border := "+" + strings.Repeat("-", width) + "+" + strings.Repeat("-", width) + "+\n"
	pad := strings.Repeat(" ", width-6)
	row := "| Item" + pad + " | " + pad + "Item |\n"
	return strings.Repeat(border+row, rows)
}

// TestEstimateCompletionTokens_WhitespaceIsNeverFree is the undercharge bound,
// and it is the one that decides how the run-collapse must be written.
//
// Collapsing every Unicode whitespace run is not safe: `unicode.IsSpace` covers
// the whole White_Space property, and several of those code points tokenize at
// ONE REAL TOKEN PER BYTE. Measured, all three encodings agree: U+1680 ogham
// space mark and U+0085 next line cost a token per byte, as do the ASCII control
// bytes U+000B vertical tab and U+000C form feed, which are ASCII whitespace and
// so are not excluded by an ASCII-only rule either. A caller who finds a route
// that omits its usage block (issue #636) can therefore send a full context
// window of these, have us pay the provider for every token of it, and be
// charged the 1-credit floor. Issue #600 makes that settlement permanent.
//
// So the estimate for caller-controlled whitespace must stay PROPORTIONAL to
// real usage: at least 5 percent of it, the same order as the 12 percent floor
// the sparsest real script lands at, rather than collapsing to nothing.
//
// The last three cases are the other side of the same choice. U+3000, U+00A0 and
// U+200B really are compressed hard by the vocabulary (48, 16 and 12 real bytes
// per token), so counting them at full byte length over-charges: U+3000 by 4.00x
// and U+00A0 by 1.33x. They rule out "collapse ASCII whitespace only" just as
// firmly as U+1680 rules out "collapse all of it".
func TestEstimateCompletionTokens_WhitespaceIsNeverFree(t *testing.T) {
	tests := []textCase{
		{
			name:      "ogham space mark",
			in:        strings.Repeat("\u1680", 40000),
			wantBytes: 120000,
			// 3 bytes each; o200k_base 120000, cl100k_base 120000, o200k_harmony 120000
			minRealTokens: 120000,
		},
		{
			name:      "next line",
			in:        strings.Repeat("\u0085", 40000),
			wantBytes: 80000,
			// 2 bytes each; all three encodings 80000 (one token per byte)
			minRealTokens: 80000,
		},
		{
			name:      "en quad",
			in:        strings.Repeat("\u2000", 20000),
			wantBytes: 60000,
			// 3 bytes each; all three encodings 40000
			minRealTokens: 40000,
		},
		{
			name:      "vertical tab",
			in:        strings.Repeat("\v", 40000),
			wantBytes: 40000,
			// all three encodings 40000 (one token per byte)
			minRealTokens: 40000,
		},
		{
			name:      "form feed",
			in:        strings.Repeat("\f", 40000),
			wantBytes: 40000,
			// all three encodings 40000 (one token per byte)
			minRealTokens: 40000,
		},
		{
			name:      "carriage return",
			in:        strings.Repeat("\r", 40000),
			wantBytes: 40000,
			// o200k_base 20000, cl100k_base 40000, o200k_harmony 20000
			minRealTokens: 20000,
		},
		{
			name:      "ideographic space",
			in:        strings.Repeat("\u3000", 20000),
			wantBytes: 60000,
			// 3 bytes each; o200k_base 1250, cl100k_base 10000, o200k_harmony 1250
			minRealTokens: 1250,
		},
		{
			name:      "no break space",
			in:        strings.Repeat("\u00a0", 20000),
			wantBytes: 40000,
			// 2 bytes each; all three encodings 2500
			minRealTokens: 2500,
		},
		{
			name:      "zero width space",
			in:        strings.Repeat("\u200b", 20000),
			wantBytes: 60000,
			// 3 bytes each; o200k_base 5000, cl100k_base 10000, o200k_harmony 5000
			minRealTokens: 5000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.check(t)
			t.Logf("%d bytes, estimate %d, real %d, %.3f of real usage", tt.wantBytes, got, tt.minRealTokens, float64(got)/float64(tt.minRealTokens))
			if got*20 < tt.minRealTokens {
				t.Errorf("estimate %d is under 5%% of real usage %d tokens (1 credit per %d real tokens): caller-controlled whitespace must not buy unbilled inference",
					got, tt.minRealTokens, tt.minRealTokens/max64(got, 1))
			}
			if got > tt.minRealTokens {
				t.Errorf("estimate %d exceeds real usage %d tokens (%.2fx): whitespace the vocabulary compresses must not be counted byte for byte",
					got, tt.minRealTokens, float64(got)/float64(tt.minRealTokens))
			}
		})
	}
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
