package utils

import (
	"math/rand"
	"time"
)

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

var randomGen *rand.Rand

func init() {
	randomGen = rand.New(rand.NewSource(time.Now().UnixNano()))
}

func RandStringRunes(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[randomGen.Intn(len(letterRunes))]
	}
	return string(b)
}
