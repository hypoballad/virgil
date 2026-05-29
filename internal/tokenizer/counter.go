package tokenizer

import (
	"math"
	"unicode"
)

const messageOverheadTokens = 8

// EstimateTokens は Ollama APIから取得できない場合のフォールバック
// ASCIIの英数字/空白、ASCII記号、非ASCIIで圧縮率を分けて概算する。
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}

	asciiWord := 0
	asciiSymbol := 0
	nonASCII := 0

	for _, r := range text {
		if r <= unicode.MaxASCII {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
				asciiWord++
			} else {
				asciiSymbol++
			}
			continue
		}
		nonASCII++
	}

	estimate := math.Ceil(
		float64(asciiWord)/4.0 +
			float64(asciiSymbol)/2.0 +
			float64(nonASCII)/1.5,
	)
	count := int(estimate) + messageOverheadTokens
	if count < 1 {
		return 1
	}
	return count
}
