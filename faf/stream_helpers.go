package faf

import (
	"math"
	"strings"
)

type FieldType byte

const (
	IntType FieldType = iota
	StringType
	FollowUpStringType
)

const (
	MaxChunkSize    = 10
	MaxStringLength = math.MaxUint16

	Delimiter = "\b"
	Tabulator = "/t"
	Linebreak = "/n"
)

func replaceSpecial(s string) string {
	s = strings.ReplaceAll(s, Tabulator, "\t")
	s = strings.ReplaceAll(s, Linebreak, "\n")
	return s
}
