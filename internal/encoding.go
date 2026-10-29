package internal

import "github.com/pgaskin/htmlstream/ascii"

//go:generate go run encoding_generate.go

// LookupEncoding returns the name of the encoding with the label, like "get
// an encoding" in the Encoding Standard: leading and trailing ASCII
// whitespace is removed, and labels are compared ASCII case-insensitively.
func LookupEncoding(label string) (string, bool) {
	name, ok := encodingLabels[ascii.ToLower(ascii.TrimSpace(label))]
	return name, ok
}
