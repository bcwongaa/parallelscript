package tests

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

const (
	stringCasesPerPart = 50 // top-level code is one C function, and clang's time grows faster than its size
	maxRandomUnits     = 9
	codePointsPerChunk = 512
	sigmaWords         = 400
)

// stringUnits are the UTF-16 code units random strings are made of: ASCII, Latin-1, a
// CJK character, JS whitespace, a capital sigma, and the two halves of U+1F600, which
// make a pair when they meet in that order and stay lone surrogates otherwise.
var stringUnits = []uint16{'a', 'b', 'c', 'A', 'B', 'x', ' ', ' ', '-', 0xE9, 0xDF, 0x3A3, 0x65E5,
	'\t', 0xA0, 0x3000, 0xFEFF, 0x2028, 0xD83D, 0xDE00, 0xD83D, 0xDE00}

// indexArguments are the index arguments the corpus passes besides small integers.
var indexArguments = []string{"NaN", "Infinity", "-Infinity", "0.5", "1.5", "-0", "-1.5", "1e10"}

type corpusPart struct {
	name     string
	seed     uint64
	generate func(rng *rand.Rand, emit func(string))
}

// stringCorpusParts generate the string corpus in parts that clang compiles in parallel.
var stringCorpusParts = append(methodCorpusParts(8),
	corpusPart{"case_bmp", 101, func(_ *rand.Rand, emit func(string)) { emitCaseMappings(0x80, 0xFFFF, emit) }},
	corpusPart{"case_supplementary", 102, func(_ *rand.Rand, emit func(string)) { emitCaseMappings(0x10000, 0x1FFFF, emit) }},
	corpusPart{"final_sigma", 103, emitFinalSigmaCases},
	corpusPart{"locale_compare_1", 104, emitLocaleCompareCases},
	corpusPart{"locale_compare_2", 105, emitLocaleCompareCases},
	corpusPart{"normalize_bmp", 106, func(_ *rand.Rand, emit func(string)) { emitNormalizations(0x80, 0xFFFF, emit) }},
	corpusPart{"normalize_supplementary", 107, func(_ *rand.Rand, emit func(string)) {
		emitNormalizations(0x10000, 0x1FFFF, emit)
		emitNormalizations(0x2F800, 0x2FA1F, emit) // the CJK compatibility ideographs, which decompose
	}},
	corpusPart{"normalize_sequences", 108, emitNormalizationSequences},
)

func methodCorpusParts(n int) []corpusPart {
	parts := make([]corpusPart, n)
	for i := range parts {
		parts[i] = corpusPart{fmt.Sprintf("methods_%d", i+1), uint64(i + 1), emitStringMethodCases}
	}
	return parts
}

// TestGeneratedStringCorpusMatchesNode is the differential corpus for strings: every
// String method with random strings (lone surrogates included) and arguments, and the
// case mappings of every code point in Unicode's first two planes, where all of them
// are, run by pts and by Node.
func TestGeneratedStringCorpusMatchesNode(t *testing.T) {
	t.Parallel()
	node := findNode(t)
	for _, part := range stringCorpusParts {
		t.Run(part.name, func(t *testing.T) {
			t.Parallel()
			source, statements := generateCorpus(rand.New(rand.NewPCG(part.seed, 13)), part.generate)
			path := filepath.Join(t.TempDir(), "string_"+part.name+".pts")
			assert.NilError(t, os.WriteFile(path, []byte(source), 0o644))
			compareWithNode(t, node, path, source, statements)
		})
	}
}

// emitStringMethodCases prints each method's result on random strings. Results go
// into a const r, and the line shows r's length and first and last code units, so a
// lone surrogate (which console.log prints as U+FFFD) is still told apart.
func emitStringMethodCases(rng *rand.Rand, emit func(string)) {
	for range stringCasesPerPart {
		s := randomUnits(rng)
		t := randomNeedle(rng, s)
		lit, needle := stringLiteral(s), stringLiteral(t)
		i, j := indexArgument(rng, len(s)), indexArgument(rng, len(s))
		show := func(label string, expr string) {
			emit(fmt.Sprintf("{ const s = %s; const t = %s; const r = %s; console.log(%q, r, r.length, r.charCodeAt(0), r.charCodeAt(r.length - 1)); }",
				lit, needle, expr, label))
		}
		emit(fmt.Sprintf("{ const s = %s; const t = %s; console.log(s.length, s[%s], s.charCodeAt(%s), s[%s] === t, t !== s[%s], `<${s[%s]}>`, s[%s] + \"|\"); }",
			lit, needle, i, i, i, j, j, i))
		emit(fmt.Sprintf("{ const s = %s; const t = %s; console.log(s.indexOf(t), s.indexOf(t, %s), s.includes(t, %s), s.startsWith(t, %s), s.endsWith(t), s.endsWith(t, %s)); }",
			lit, needle, i, j, i, j))
		show("slice", fmt.Sprintf("s.slice(%s, %s)", i, j))
		show("slice1", fmt.Sprintf("s.slice(%s)", i))
		show("substring", fmt.Sprintf("s.substring(%s, %s)", i, j))
		show("upper", "s.toUpperCase()")
		show("lower", "s.toLowerCase()")
		show("trim", "s.trim()")
		show("trimStart", "s.trimStart()")
		show("trimEnd", "s.trimEnd()")
		show("padStart", fmt.Sprintf("s.padStart(%d, t)", rng.IntN(14)))
		show("padEnd", fmt.Sprintf("s.padEnd(%d, t)", rng.IntN(14)))
		show("padEndSpace", fmt.Sprintf("s.padEnd(%d)", rng.IntN(14)))
		show("repeat", fmt.Sprintf("s.repeat(%d)", rng.IntN(4)))
		emit(fmt.Sprintf("{ const s = %s; const t = %s; console.log(s.lastIndexOf(t), s.lastIndexOf(t, %s), s.at(%s), s.charAt(%s), s.codePointAt(%s), s.isWellFormed()); }",
			lit, needle, i, i, j, i))
		show("substr", fmt.Sprintf("s.substr(%s, %s)", i, j))
		show("replace", `s.replace(t, "<$&|$'>")`)
		show("replaceAll", "s.replaceAll(t, \"_\")")
		show("replaceAllCallback", "s.replaceAll(t, (m, i) => `${i}`)")
		show("concat", "s.concat(t, s)")
		show("toWellFormed", "s.toWellFormed()")
	}
}

// collationLetters are what the localeCompare cases' words are made of: Latin letters in
// both cases, with accents and combining marks, digits, punctuation, spaces, Greek,
// Cyrillic, Han, Hangul, an emoji and a control character. Letters that ICU expands
// (ß, æ) are left out: they compare as letters of their own (D-082).
var collationLetters = []string{"a", "b", "c", "e", "z", "A", "B", "E", "Z", "é", "É", "è", "ê", "à", "ç", "ñ", "ü", "ö", "ø", "å",
	"0", "1", "9", "-", "_", " ", ".", "'", ",", "α", "Α", "я", "Я", "中", "文", "한", "글", "각", "😀", "\\u0301", "\\u0001"}

// emitLocaleCompareCases sorts lists of random words with localeCompare and compares
// random pairs.
func emitLocaleCompareCases(rng *rand.Rand, emit func(string)) {
	word := func() string {
		var w strings.Builder
		for range 1 + rng.IntN(5) {
			w.WriteString(collationLetters[rng.IntN(len(collationLetters))])
		}
		return `"` + w.String() + `"`
	}
	for range stringCasesPerPart {
		words := make([]string, 12)
		for i := range words {
			words[i] = word()
		}
		emit(fmt.Sprintf("console.log([%s].sort((a, b) => a.localeCompare(b)).join(\"|\"));", strings.Join(words, ", ")))
		emit(fmt.Sprintf("console.log(%s.localeCompare(%s), %s.localeCompare(%s));", words[0], words[1], words[2], strings.ToUpper(words[2])))
	}
}

// emitCaseMappings upper- and lowercases every code point in [first, last], skipping
// surrogates and the C1 controls, a chunk per line.
func emitCaseMappings(first rune, last rune, emit func(string)) {
	var chunk strings.Builder
	count := 0
	flush := func() {
		if count > 0 {
			emit(fmt.Sprintf("console.log(\"%s\".toUpperCase());", chunk.String()))
			emit(fmt.Sprintf("console.log(\"%s\".toLowerCase());", chunk.String()))
		}
		chunk.Reset()
		count = 0
	}
	for cp := first; cp <= last; cp++ {
		if (cp >= 0xD800 && cp <= 0xDFFF) || (cp >= 0x80 && cp <= 0x9F) {
			continue
		}
		fmt.Fprintf(&chunk, "\\u{%X}", cp)
		if count++; count == codePointsPerChunk {
			flush()
		}
	}
	flush()
}

// emitNormalizations normalizes every code point in [first, last] in the four forms,
// skipping surrogates and the C1 controls, a chunk per line (D-135).
func emitNormalizations(first rune, last rune, emit func(string)) {
	var chunk strings.Builder
	count := 0
	flush := func() {
		if count > 0 {
			emit(fmt.Sprintf("{ const s = \"%s\"; console.log(s.normalize(\"NFC\"), s.normalize(\"NFD\"), s.normalize(\"NFKC\"), s.normalize(\"NFKD\")); }", chunk.String()))
		}
		chunk.Reset()
		count = 0
	}
	for cp := first; cp <= last; cp++ {
		if (cp >= 0xD800 && cp <= 0xDFFF) || (cp >= 0x80 && cp <= 0x9F) {
			continue
		}
		fmt.Fprintf(&chunk, "\\u{%X}", cp)
		if count++; count == codePointsPerChunk {
			flush()
		}
	}
	flush()
}

// normalizationPieces are what the normalization sequences are made of: starters that
// compose (Latin letters, precomposed ones, Hangul jamo and syllables, Tamil and Tibetan
// pieces), marks of many combining classes in every order, compatibility characters, a
// lone surrogate and an astral letter.
var normalizationPieces = []string{"a", "e", "o", "u", "c", "s", "A", "\\u00C5", "\\u00E7", "\\u1E63", "\\u0301", "\\u0300", "\\u0308",
	"\\u0327", "\\u0328", "\\u0323", "\\u0334", "\\u0345", "\\u0315", "\\u031B", "\\u05B0", "\\u0F72", "\\u0F71", "\\u0F80",
	"\\u1100", "\\u1161", "\\u11A8", "\\uAC00", "\\uD55C", "\\u0B92", "\\u0BD7", "\\uFB01", "\\u2460", "\\uFF76", "\\uFF9E", "\\u3131",
	"\\u212B", "\\u0344", "\\uD83D", "𝐀", " "} // the astral letter raw: Node's parser rejects "\\uD83D\\u{1D400}"

// emitNormalizationSequences normalizes random sequences of normalizationPieces in the
// four forms: canonical ordering, composition and its blocking, Hangul.
func emitNormalizationSequences(rng *rand.Rand, emit func(string)) {
	for range sigmaWords {
		var word strings.Builder
		for range 1 + rng.IntN(7) {
			word.WriteString(normalizationPieces[rng.IntN(len(normalizationPieces))])
		}
		w := word.String()
		emit(fmt.Sprintf("{ const s = \"%s\"; console.log(s.normalize(), s.normalize(\"NFD\"), s.normalize(\"NFKC\"), s.normalize(\"NFKD\"), s.normalize().length); }", w))
	}
}

// sigmaContext are the characters around capital sigmas in the Final_Sigma cases: cased
// letters (an astral one too, written raw because Node's TypeScript parser rejects "\\uD83D\\u{10400}"), case-ignorable ones (".", "'", combining marks, U+0345,
// which is both), and characters that are neither.
var sigmaContext = []string{"Σ", "Σ", "Α", "a", "σ", ".", "'", "\\u0301", "\\u0345", " ", "1", "𐐀", "\\uD83D", "\\u00AD"}

func emitFinalSigmaCases(rng *rand.Rand, emit func(string)) {
	for range sigmaWords {
		var word strings.Builder
		for range 1 + rng.IntN(6) {
			word.WriteString(sigmaContext[rng.IntN(len(sigmaContext))])
		}
		emit(fmt.Sprintf("console.log(\"%s\".toLowerCase(), \"%s\".toUpperCase());", word.String(), word.String()))
	}
}

func randomUnits(rng *rand.Rand) []uint16 {
	return randomUnitsOfLength(rng, rng.IntN(maxRandomUnits+1))
}

func randomUnitsOfLength(rng *rand.Rand, n int) []uint16 {
	units := make([]uint16, n)
	for i := range units {
		units[i] = stringUnits[rng.IntN(len(stringUnits))]
	}
	return units
}

// randomNeedle is a search string for s: usually a piece of it (often a half of a pair), sometimes random.
func randomNeedle(rng *rand.Rand, s []uint16) []uint16 {
	if len(s) == 0 || rng.IntN(4) == 0 {
		return randomUnitsOfLength(rng, rng.IntN(3))
	}
	start := rng.IntN(len(s))
	return s[start : start+rng.IntN(min(3, len(s)-start)+1)]
}

// indexArgument is an index for a string of n units: mostly an integer near the range, sometimes a special number.
func indexArgument(rng *rand.Rand, n int) string {
	if rng.IntN(5) == 0 {
		return indexArguments[rng.IntN(len(indexArguments))]
	}
	return strconv.Itoa(rng.IntN(n+5) - 2)
}

// stringLiteral writes UTF-16 units as a TypeScript string literal, escaping every non-ASCII unit.
func stringLiteral(units []uint16) string {
	var literal strings.Builder
	literal.WriteByte('"')
	for _, unit := range units {
		if unit >= ' ' && unit < 0x7F && unit != '"' && unit != '\\' {
			literal.WriteByte(byte(unit))
		} else {
			fmt.Fprintf(&literal, "\\u%04X", unit)
		}
	}
	literal.WriteByte('"')
	return literal.String()
}
