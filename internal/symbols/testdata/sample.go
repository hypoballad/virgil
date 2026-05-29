package sample

import (
	"fmt"
	"strings"
)

// Add は2つの整数を加算する
func Add(a, b int) int {
	return a + b
}

// Sub は減算する
func Sub(a, b int) int {
	return a - b
}

// Calculator は計算機を表す
type Calculator struct {
	value int
}

// NewCalculator は Calculator を生成する
func NewCalculator() *Calculator {
	return &Calculator{}
}

// Increment はカウンタをインクリメントする
func (c *Calculator) Increment() {
	c.value++
}

// Value は現在の値を返す
func (c *Calculator) Value() int {
	return c.value
}

// Greeter は挨拶インターフェース
type Greeter interface {
	Greet() string
}

// MaxValue は最大値の定数
const MaxValue = 100

// CurrentVersion はバージョン文字列
var CurrentVersion = "v1.0.0"

// helperFunction は内部ヘルパー
func helperFunction() {
	// ローカル変数（抽出されてはならない）
	var localBuffer strings.Builder
	const localPrefix = "[helper]"

	localBuffer.WriteString(localPrefix)
	fmt.Println(localBuffer.String(), "helper")
}
