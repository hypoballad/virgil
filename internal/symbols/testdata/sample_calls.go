package sample_calls

import "fmt"

// トップレベルの呼び出し（init 内）
func init() {
	fmt.Println("init")
	setup()
}

// 通常の関数呼び出し
func main() {
	greet("world")
	value := compute(10)
	fmt.Println(value)
}

func greet(name string) {
	fmt.Println("hello", name)
}

func compute(x int) int {
	return double(x) + 1
}

func double(x int) int {
	return x * 2
}

func setup() {
	fmt.Println("setup")
}

// メソッド呼び出し
type Counter struct {
	value int
}

func (c *Counter) Increment() {
	c.value++
	c.log()
}

func (c *Counter) log() {
	fmt.Println("count:", c.value)
}

func useCounter() {
	c := &Counter{}
	c.Increment()
	c.Increment()
}
