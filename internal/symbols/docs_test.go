package symbols

import "testing"

func TestAttachSymbolDocsPythonDocstrings(t *testing.T) {
	src := []byte(`class Service:
    """Coordinates work.

    Keeps the public API small.
    """

    def run(self):
        """Run one cycle."""
        return True

def helper():
    # not a docstring
    return None
`)
	symbols, err := NewExtractor().ExtractFromSource(src, "python")
	if err != nil {
		t.Fatalf("ExtractFromSource failed: %v", err)
	}

	byName := map[string]Symbol{}
	for _, sym := range symbols {
		byName[sym.Receiver+"."+sym.Name] = sym
		byName[sym.Name] = sym
	}

	if got := byName["Service"].Doc; got != "Coordinates work. Keeps the public API small." {
		t.Fatalf("Service doc = %q", got)
	}
	if got := byName["Service.run"].Doc; got != "Run one cycle." {
		t.Fatalf("Service.run doc = %q", got)
	}
	if got := byName["helper"].Doc; got != "" {
		t.Fatalf("helper should not use body comment as doc, got %q", got)
	}
}

func TestAttachSymbolDocsLeadingComments(t *testing.T) {
	src := []byte(`package sample

// Worker performs jobs.
// It is safe for reuse.
type Worker struct{}

// Run starts work.
func Run() {}
`)
	symbols, err := NewExtractor().ExtractFromSource(src, "go")
	if err != nil {
		t.Fatalf("ExtractFromSource failed: %v", err)
	}

	docs := map[string]string{}
	for _, sym := range symbols {
		docs[sym.Name] = sym.Doc
	}
	if got := docs["Worker"]; got != "Worker performs jobs. It is safe for reuse." {
		t.Fatalf("Worker doc = %q", got)
	}
	if got := docs["Run"]; got != "Run starts work." {
		t.Fatalf("Run doc = %q", got)
	}
}
