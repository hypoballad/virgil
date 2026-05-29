package tokenizer

import "testing"

func TestEstimateTokensWeightedHeuristic(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{
			name: "empty",
			text: "",
			want: 0,
		},
		{
			name: "ascii word includes overhead",
			text: "hello world",
			want: 11,
		},
		{
			name: "ascii symbols are weighted separately",
			text: "{}[](),.:",
			want: 13,
		},
		{
			name: "non ascii",
			text: "防御的TODO",
			want: 11,
		},
		{
			name: "mixed code and japanese",
			text: "func Add(a, b int) int { return a + b } // 防御",
			want: 23,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EstimateTokens(tt.text); got != tt.want {
				t.Fatalf("EstimateTokens(%q) = %d, want %d", tt.text, got, tt.want)
			}
		})
	}
}
