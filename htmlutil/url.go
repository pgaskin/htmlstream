package htmlutil

import (
	"iter"
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/pgaskin/htmlstream"
	"github.com/pgaskin/htmlstream/ascii"
	"github.com/pgaskin/htmlstream/internal"
)

// CleanURL removes leading and trailing C0 control characters and spaces,
// and all ASCII tabs and newlines, following the first steps of the
// [URL Standard] parser.
//
// [URL Standard]: https://url.spec.whatwg.org/#concept-basic-url-parser
func CleanURL(s string) string {
	s = strings.TrimFunc(s, func(c rune) bool { return c <= ' ' })
	if strings.ContainsAny(s, "\t\n\r") {
		s = strings.Map(func(c rune) rune {
			if c == '\t' || c == '\n' || c == '\r' {
				return -1
			}
			return c
		}, s)
	}
	return s
}

// ResolveURL parses ref (after [CleanURL]) and resolves it against base, if
// not nil. Unlike [url.URL.ResolveReference], the result only has a fragment
// if ref has one, like in browsers.
//
// It parses URLs with [url.Parse], not the [URL Standard] parser browsers
// use, so some URLs are parsed differently: e.g., backslashes in http and
// https URLs aren't read as slashes, hosts aren't normalized (IDNA, IPv4
// address forms), and characters aren't percent-encoded the same way. Some
// URLs browsers accept are errors.
//
// [URL Standard]: https://url.spec.whatwg.org/#concept-basic-url-parser
func ResolveURL(base *url.URL, ref string) (*url.URL, error) {
	u, err := url.Parse(CleanURL(ref))
	if err != nil || base == nil {
		return u, err
	}
	r := base.ResolveReference(u)
	r.Fragment, r.RawFragment = u.Fragment, u.RawFragment
	return r, nil
}

// BaseURL tracks the base URL of an HTML document like the [HTML Standard]
// defines it (the frozen base URL): the href of the first base element with
// one, resolved against the document URL. Base elements in template
// contents, which HTML parsers put in a separate document fragment, are
// left out.
//
// In a single pass, links before the base element can't be resolved against
// it. A base element is usually in the head, so links can be held until the
// head ends (see [Document]).
//
// The zero value is like the BaseURL [NewBaseURL] returns for a nil document
// URL.
//
// [HTML Standard]: https://html.spec.whatwg.org/multipage/urls-and-fetching.html#frozen-base-url
type BaseURL struct {
	doc      *url.URL
	base     *url.URL
	seen     bool
	template Stack[struct{}] // open template elements
}

// NewBaseURL returns a BaseURL for a document at the URL doc (which may be
// nil).
func NewBaseURL(doc *url.URL) *BaseURL {
	return &BaseURL{doc: doc, base: doc}
}

// Update processes the next token, returning true if it set the base URL.
// It must be called with every start and end tag, to track template
// elements. A base href which can't be parsed (see [ResolveURL]), or is a
// data: or javascript: URL, leaves the document URL as the base URL, like
// in browsers.
func (b *BaseURL) Update(tok *htmlstream.Token) bool {
	if b.seen {
		return false
	}
	switch tok.Kind() {
	case htmlstream.StartTag:
		if tok.NS() != htmlstream.NamespaceXHTML {
			return false
		}
		if isLocal(tok, "template") {
			b.template.Push(tok, struct{}{})
			return false
		}
		if b.template.Len() != 0 || !isLocal(tok, "base") {
			return false
		}
	case htmlstream.EndTag:
		b.template.Pop(tok)
		return false
	default:
		return false
	}
	a, ok := tok.Attr("href")
	if !ok {
		return false
	}
	b.seen = true
	u, err := ResolveURL(b.doc, a.Value())
	if err != nil || u.Scheme == "data" || u.Scheme == "javascript" {
		return false
	}
	b.base = u
	return true
}

// Seen reports whether a base element with an href was seen. Later base
// elements don't change the base URL.
func (b *BaseURL) Seen() bool { return b.seen }

// URL returns the current base URL, or nil if the document URL is nil and
// no base element set one.
func (b *BaseURL) URL() *url.URL { return b.base }

// Resolve resolves ref against the current base URL (see [ResolveURL]), which
// is nil if the document URL is nil and no base element set one.
func (b *BaseURL) Resolve(ref string) (*url.URL, error) {
	return ResolveURL(b.base, ref)
}

// URLAttrs returns an iterator over the lowercase names and the attributes
// of a start tag whose values are URLs (e.g., a href, img src), or srcset
// lists of URLs (see [IsSrcset]).
//
// For HTML elements, these are the attributes the [HTML Standard] defines
// as URLs or srcset lists: a, area, base and link href; img, script,
// iframe, embed, audio, video, source, track and input src; img and source
// srcset and link imagesrcset; video poster; object data; form action;
// button and input formaction; and blockquote, q, del and ins cite. The
// ping attribute (a list of URLs) and the microdata itemid, itemprop and
// itemtype attributes (identifiers) aren't included. Of the obsolete
// attributes, it includes frame src; img, iframe and frame longdesc; and
// background, which browsers load on the elements the [rendering section]
// lists (body, table, thead, tbody, tfoot, tr, td and th), but not others
// like lowsrc, html manifest and object codebase. An image element in the
// XHTML namespace has the URL attributes of img under HTML rules (see
// [htmlstream.Token.HTMLRules]), as HTML parsers turn it into img.
//
// For the SVG elements a, feImage, image, script and use, the URL attribute is
// href, or if there is no href, xlink:href. [SVG 2] specifies that xlink:href
// is ignored if href is present, as browsers do. Under HTML rules, xlink:href
// is the attribute with that name, which HTML parsers put in the XLink
// namespace; an attribute with another prefix is in no namespace for them,
// whatever it is bound to. Otherwise, it is the href attribute in the XLink
// namespace (see [htmlstream.Token.AttrNamespace]), whose name is given as
// "xlink:href" whatever its prefix.
//
// Other elements, and URL attributes which usually refer to the same
// document (like the href of SVG gradients and animation elements), are not
// included.
//
// [HTML Standard]: https://html.spec.whatwg.org/multipage/indices.html#attributes-1
// [rendering section]: https://html.spec.whatwg.org/multipage/rendering.html#tables-2
// [SVG 2]: https://www.w3.org/TR/SVG2/linking.html#XLinkRefAttrs
func URLAttrs(tok *htmlstream.Token) iter.Seq2[string, htmlstream.Attr] {
	return func(yield func(string, htmlstream.Attr) bool) {
		if tok.Kind() != htmlstream.StartTag {
			return
		}
		switch tok.NS() {
		case htmlstream.NamespaceXHTML:
			var buf [16]byte
			lname := string(tok.AppendLocalName(buf[:0]))
			if lname == "image" && tok.HTMLRules() {
				// HTML parsers turn image into img; XML parsers keep image.
				lname = "img"
			}
			names := internal.URLAttrs[lname]
			for _, n := range names {
				if a, ok := tok.Attr(n); ok && !yield(n, a) {
					return
				}
			}
		case htmlstream.NamespaceSVG:
			for _, n := range internal.SVGURLElems {
				if !tok.LocalIs(n) {
					continue
				}
				if a, ok := tok.Attr("href"); ok {
					yield("href", a)
				} else if a, ok := xlinkHref(tok); ok {
					yield("xlink:href", a)
				}
				return
			}
		}
	}
}

// xlinkHref returns the xlink:href attribute of an SVG element (see
// URLAttrs).
func xlinkHref(tok *htmlstream.Token) (htmlstream.Attr, bool) {
	if tok.HTMLRules() {
		return tok.Attr("xlink:href")
	}
	return tok.AttrNS(htmlstream.NamespaceXLink, "href")
}

// IsSrcset reports whether an attribute contains a srcset list of URLs (see
// [ParseSrcset]).
func IsSrcset(a htmlstream.Attr) bool {
	return a.Is("srcset") || a.Is("imagesrcset")
}

// SrcsetCandidate is an image candidate in a srcset attribute.
type SrcsetCandidate struct {
	// URL is the URL as it is in the attribute value, without trailing
	// commas. It isn't resolved (see [ResolveURL]).
	URL string

	// Descriptor is the descriptors as written, separated by single spaces
	// (e.g., "2x", "100w" or "100w 50h"), or empty if there are none.
	Descriptor string

	// Width is the value of the w descriptor, or 0 if there is none.
	Width int

	// Density is the value of the x descriptor. If there is neither a w nor
	// an x descriptor, it is 1, the density the [HTML Standard] gives the
	// candidate when [normalizing the source densities]. If there is a w
	// descriptor, it is 0. It is never negative, but may be 0 with no w
	// descriptor ("0x").
	//
	// [HTML Standard]: https://html.spec.whatwg.org/multipage/images.html#parse-a-srcset-attribute
	// [normalizing the source densities]: https://html.spec.whatwg.org/multipage/images.html#normalise-the-source-densities
	Density float64

	// Height is the value of the h descriptor (future-compat-h in the
	// [HTML Standard]), or 0 if there is none. It is only valid with a w
	// descriptor, and isn't used to select an image.
	//
	// [HTML Standard]: https://html.spec.whatwg.org/multipage/images.html#parse-a-srcset-attribute
	Height int
}

// ParseSrcset returns an iterator over the image candidates in a srcset
// attribute value, following the [HTML Standard] algorithm to parse a srcset
// attribute. Like in browsers, candidates with invalid descriptors are left
// out. A candidate can have no descriptors, an x descriptor, a w
// descriptor, or a w and an h descriptor in either order. The value of a w
// or h descriptor is a non-zero valid non-negative integer (ASCII digits,
// e.g., "100w" or "080h"), and that of an x descriptor is a non-negative
// valid floating-point number (e.g., "2x", ".5x", "1.5e1x" or "-0x", but
// not "1.x", "+1x" or "-1x"). Any other descriptor is invalid, including
// descriptors in another case ("2X") and with parentheses ("(max-width:
// 1px)"). An x value too large for a float64 is invalid, as the rules for
// parsing floating-point number values return an error for it.
//
// It differs from the HTML Standard in that w and h values too large for an
// int are clamped to [math.MaxInt]. It only parses the attribute: selecting
// an image, which adds the img src attribute as a 1x candidate, computes the
// density of w candidates from the sizes attribute, and ignores candidates
// with the same density as an earlier one, isn't done.
//
// [HTML Standard]: https://html.spec.whatwg.org/multipage/images.html#parse-a-srcset-attribute
func ParseSrcset(v string) iter.Seq[SrcsetCandidate] {
	return func(yield func(SrcsetCandidate) bool) {
		i := 0
		for {
			for i < len(v) && (ascii.IsSpace(v[i]) || v[i] == ',') {
				i++
			}
			if i == len(v) {
				return
			}
			j := i
			for j < len(v) && !ascii.IsSpace(v[j]) {
				j++
			}
			c := SrcsetCandidate{URL: v[i:j]}
			i = j
			ok := true
			if strings.HasSuffix(c.URL, ",") {
				c.URL = strings.TrimRight(c.URL, ",")
				c.Density = 1
			} else {
				ok, i = srcsetDescriptors(&c, v, i)
			}
			if ok && !yield(c) {
				return
			}
		}
	}
}

// srcsetDescriptors reads the descriptors starting at i and parses them
// into c, returning whether they are valid and the position after them.
func srcsetDescriptors(c *SrcsetCandidate, v string, i int) (bool, int) {
	var (
		d   [2]string // a valid candidate has at most two descriptors
		pos [2]int    // the positions of d in v
		n   int       // the number of descriptors
		end = len(v)
	)
	add := func(start, end int) {
		if start < end {
			if n < len(d) {
				d[n], pos[n] = v[start:end], start
			}
			n++
		}
	}
	for i < len(v) && ascii.IsSpace(v[i]) {
		i++
	}
	start, parens := i, false
	for ; i < len(v); i++ {
		b := v[i]
		if parens {
			parens = b != ')'
			continue
		}
		if b == ',' {
			add(start, i)
			end = i + 1
			break
		}
		if b == '(' {
			parens = true
		} else if ascii.IsSpace(b) {
			add(start, i)
			for i+1 < len(v) && ascii.IsSpace(v[i+1]) {
				i++
			}
			start = i + 1
		}
	}
	if i == len(v) {
		add(start, i)
	}
	if n > len(d) {
		return false, end
	}
	var hasW, hasX, hasH bool
	for _, s := range d[:n] {
		num := s[:len(s)-1]
		switch k := s[len(s)-1]; {
		case k == 'w' && isSrcsetInt(num):
			if hasW || hasX {
				return false, end
			}
			c.Width, hasW = srcsetInt(num), true
		case k == 'x' && isSrcsetFloat(num):
			if hasW || hasX || hasH {
				return false, end
			}
			x, err := strconv.ParseFloat(num, 64)
			if err != nil || x < 0 {
				return false, end
			}
			if x == 0 {
				x = 0 // not -0
			}
			c.Density, hasX = x, true
		case k == 'h' && isSrcsetInt(num):
			if hasH || hasX {
				return false, end
			}
			c.Height, hasH = srcsetInt(num), true
		default:
			return false, end
		}
	}
	if hasW && c.Width == 0 || hasH && (c.Height == 0 || !hasW) {
		return false, end
	}
	if !hasW && !hasX {
		c.Density = 1
	}
	switch {
	case n == 1:
		c.Descriptor = d[0]
	case n == 2 && pos[1] == pos[0]+len(d[0])+1 && v[pos[1]-1] == ' ':
		c.Descriptor = v[pos[0] : pos[1]+len(d[1])]
	case n == 2:
		c.Descriptor = d[0] + " " + d[1]
	}
	return true, end
}

// isSrcsetInt reports whether s is a valid non-negative integer.
func isSrcsetInt(s string) bool {
	return s != "" && srcsetDigits(s, 0) == len(s)
}

// isSrcsetFloat reports whether s is a valid floating-point number.
func isSrcsetFloat(s string) bool {
	i := 0
	if i < len(s) && s[i] == '-' {
		i++
	}
	j := srcsetDigits(s, i)
	mantissa := j > i
	if i = j; i < len(s) && s[i] == '.' {
		if j = srcsetDigits(s, i+1); j == i+1 {
			return false
		}
		i, mantissa = j, true
	}
	if !mantissa {
		return false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		if i++; i < len(s) && (s[i] == '-' || s[i] == '+') {
			i++
		}
		if j = srcsetDigits(s, i); j == i {
			return false
		}
		i = j
	}
	return i == len(s)
}

// srcsetDigits returns the position after the ASCII digits in s starting
// at i.
func srcsetDigits(s string, i int) int {
	for i < len(s) && '0' <= s[i] && s[i] <= '9' {
		i++
	}
	return i
}

// srcsetInt parses a valid non-negative integer, clamping it to
// math.MaxInt.
func srcsetInt(s string) int {
	var n int
	for _, b := range []byte(s) {
		d := int(b - '0')
		if n > (math.MaxInt-d)/10 {
			return math.MaxInt
		}
		n = n*10 + d
	}
	return n
}
