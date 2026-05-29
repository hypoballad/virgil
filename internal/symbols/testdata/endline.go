package sample

func singleLine() {}

func multiLine(a int) int {
	if a > 0 {
		return a
	}
	return 0
}

type EndLineReader interface {
	ReadLine() (string, error)
	Close() error
}
