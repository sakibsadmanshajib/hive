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
