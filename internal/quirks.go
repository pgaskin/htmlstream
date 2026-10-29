package internal

import (
	"bytes"
	"strings"

	"github.com/pgaskin/htmlstream/ascii"
)

// Doctype is a doctype as HTML parsers read it.
type Doctype struct {
	Name                 string // ASCII-lowercased, or empty if missing
	Public, System       string // identifiers
	HasPublic, HasSystem bool
	ForceQuirks          bool // the tokenizer's force-quirks flag (a malformed doctype)
}

// ParseDoctype parses the data of a doctype (between "<!DOCTYPE" and ">")
// like the HTML tokenizer. NUL characters are replaced with U+FFFD.
func ParseDoctype(data []byte) Doctype {
	var d Doctype
	b := data
	skip := func() {
		for len(b) > 0 && ascii.IsSpace(b[0]) {
			b = b[1:]
		}
	}
	str := func(s []byte) string {
		if bytes.IndexByte(s, 0) >= 0 {
			return string(bytes.ReplaceAll(s, []byte{0}, []byte("\uFFFD")))
		}
		return string(s)
	}
	// quoted reads the identifier at b, after optional whitespace. Anything
	// else, or a missing end quote (the doctype ends at the first '>'),
	// sets the force-quirks flag, and the rest is ignored.
	quoted := func(id *string, has *bool) bool {
		skip()
		if len(b) == 0 || b[0] != '"' && b[0] != '\'' {
			d.ForceQuirks = true
			return false
		}
		*has = true
		i := bytes.IndexByte(b[1:], b[0])
		if i < 0 {
			*id = str(b[1:])
			d.ForceQuirks = true
			return false
		}
		*id = str(b[1 : 1+i])
		b = b[2+i:]
		return true
	}
	keyword := func(k string) bool {
		if len(b) >= len(k) && ascii.EqualFold(b[:len(k)], k) {
			b = b[len(k):]
			return true
		}
		return false
	}
	skip()
	i := 0
	for i < len(b) && !ascii.IsSpace(b[i]) {
		i++
	}
	if i == 0 {
		d.ForceQuirks = true
		return d
	}
	d.Name = str(ascii.AppendLower(nil, b[:i]))
	b = b[i:]
	skip()
	switch {
	case len(b) == 0:
	case keyword("public"):
		if quoted(&d.Public, &d.HasPublic) {
			if skip(); len(b) > 0 {
				quoted(&d.System, &d.HasSystem)
			}
		}
	case keyword("system"):
		quoted(&d.System, &d.HasSystem)
		// anything after the system identifier is ignored
	default:
		d.ForceQuirks = true
	}
	return d
}

// Document modes selected by doctypes.
const (
	NoQuirks = iota
	LimitedQuirks
	Quirks
)

// QuirksMode returns the document mode the doctype selects in HTML parsers,
// following [the initial insertion mode] (see quirksPublicIDs).
//
// [the initial insertion mode]: https://html.spec.whatwg.org/multipage/parsing.html#the-initial-insertion-mode
func (d Doctype) QuirksMode() int {
	if d.ForceQuirks || d.Name != "html" {
		return Quirks
	}
	// identifiers are compared ASCII case-insensitively
	public, system := ascii.ToLower(d.Public), ascii.ToLower(d.System)
	if d.HasSystem && system == "http://www.ibm.com/data/dtd/v11/ibmxhtml1-transitional.dtd" {
		return Quirks // whatever the public identifier
	}
	if d.HasPublic {
		switch public {
		case "-//w3o//dtd w3 html strict 3.0//en//", "-/w3c/dtd html 4.0 transitional/en", "html":
			return Quirks
		}
		for _, q := range quirksPublicIDs {
			if strings.HasPrefix(public, q) {
				return Quirks
			}
		}
		html401 := strings.HasPrefix(public, "-//w3c//dtd html 4.01 frameset//") || strings.HasPrefix(public, "-//w3c//dtd html 4.01 transitional//")
		if html401 && !d.HasSystem {
			return Quirks
		}
		if html401 || strings.HasPrefix(public, "-//w3c//dtd xhtml 1.0 frameset//") || strings.HasPrefix(public, "-//w3c//dtd xhtml 1.0 transitional//") {
			return LimitedQuirks
		}
	}
	return NoQuirks
}

// QuirksDoctype reports whether the data of a complete doctype (between
// "<!DOCTYPE" and ">") selects quirks mode in HTML parsers.
func QuirksDoctype(data []byte) bool {
	return ParseDoctype(data).QuirksMode() == Quirks
}

// quirksPublicIDs are the prefixes of public identifiers which select quirks
// mode, lowercased. They are the list in [the initial insertion mode] of the
// HTML Standard, in the same order.
//
// [the initial insertion mode]: https://html.spec.whatwg.org/multipage/parsing.html#the-initial-insertion-mode
var quirksPublicIDs = []string{
	"+//silmaril//dtd html pro v0r11 19970101//",
	"-//as//dtd html 3.0 aswedit + extensions//",
	"-//advasoft ltd//dtd html 3.0 aswedit + extensions//",
	"-//ietf//dtd html 2.0 level 1//",
	"-//ietf//dtd html 2.0 level 2//",
	"-//ietf//dtd html 2.0 strict level 1//",
	"-//ietf//dtd html 2.0 strict level 2//",
	"-//ietf//dtd html 2.0 strict//",
	"-//ietf//dtd html 2.0//",
	"-//ietf//dtd html 2.1e//",
	"-//ietf//dtd html 3.0//",
	"-//ietf//dtd html 3.2 final//",
	"-//ietf//dtd html 3.2//",
	"-//ietf//dtd html 3//",
	"-//ietf//dtd html level 0//",
	"-//ietf//dtd html level 1//",
	"-//ietf//dtd html level 2//",
	"-//ietf//dtd html level 3//",
	"-//ietf//dtd html strict level 0//",
	"-//ietf//dtd html strict level 1//",
	"-//ietf//dtd html strict level 2//",
	"-//ietf//dtd html strict level 3//",
	"-//ietf//dtd html strict//",
	"-//ietf//dtd html//",
	"-//metrius//dtd metrius presentational//",
	"-//microsoft//dtd internet explorer 2.0 html strict//",
	"-//microsoft//dtd internet explorer 2.0 html//",
	"-//microsoft//dtd internet explorer 2.0 tables//",
	"-//microsoft//dtd internet explorer 3.0 html strict//",
	"-//microsoft//dtd internet explorer 3.0 html//",
	"-//microsoft//dtd internet explorer 3.0 tables//",
	"-//netscape comm. corp.//dtd html//",
	"-//netscape comm. corp.//dtd strict html//",
	"-//o'reilly and associates//dtd html 2.0//",
	"-//o'reilly and associates//dtd html extended 1.0//",
	"-//o'reilly and associates//dtd html extended relaxed 1.0//",
	"-//sq//dtd html 2.0 hotmetal + extensions//",
	"-//softquad software//dtd hotmetal pro 6.0::19990601::extensions to html 4.0//",
	"-//softquad//dtd hotmetal pro 4.0::19971010::extensions to html 4.0//",
	"-//spyglass//dtd html 2.0 extended//",
	"-//sun microsystems corp.//dtd hotjava html//",
	"-//sun microsystems corp.//dtd hotjava strict html//",
	"-//w3c//dtd html 3 1995-03-24//",
	"-//w3c//dtd html 3.2 draft//",
	"-//w3c//dtd html 3.2 final//",
	"-//w3c//dtd html 3.2//",
	"-//w3c//dtd html 3.2s draft//",
	"-//w3c//dtd html 4.0 frameset//",
	"-//w3c//dtd html 4.0 transitional//",
	"-//w3c//dtd html experimental 19960712//",
	"-//w3c//dtd html experimental 970421//",
	"-//w3c//dtd w3 html//",
	"-//w3o//dtd w3 html 3.0//",
	"-//webtechs//dtd mozilla html 2.0//",
	"-//webtechs//dtd mozilla html//",
}
