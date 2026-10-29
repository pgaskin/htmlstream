# htmlstream

Go library for robust streaming html/xhtml/xml parsing and transformation, suitable for untrusted/malformed input.

#### Features

- Token-level context-aware streaming HTML/XHTML/XML parser.
- Support for emitting polyglot HTML/XHTML/XML output.
- Hybrid HTML/XHTML parse mode (tolerating XMLisms in HTML).
- DOM-like helpers for HTML parsing.
- Optional simplified SAX-style wrapper.
- Byte-identical decode/encode roundtrip by default.
- Good tolerance for malformed input.
- Streaming transformations with backtracking.
- Fast and allocation-efficient.
- Works with plain XML too.
- Good test coverage and fuzzing.
- Parser can be used with stdlib `encoding/xml`.

#### Packages

The [`htmlstream`](https://pkg.go.dev/github.com/pgaskin/htmlstream) package reads and transforms HTML/XHTML as a context-aware stream of tokens. By default, it is byte-exact unless modified, even for malformed input. It is safe to use with untrusted content without using unbounded memory or crashing. It supports multiple modes, including a universal mode which is able to parse HTML while tolerating XMLisms (or parsing it as XML if it has an explicit `<?xml` declaration and `xmlns`) (it is not intended for arbitrary XML, there's a specific mode for that). It will automatically balance unclosed or misnested HTML5 tags almost exactly the same as browsers (some cases are slightly different since it works in a single pass without building the full tree). There are some helpers for matching elements.

The [`htmlstream`](https://pkg.go.dev/github.com/pgaskin/htmlstream) package is also capable of transforming XML, but provides limited XML-specific functionality (e.g., no doctype processing, very limited namespace awareness, no checks for well-formedness, no check for XML name validity). Notably, it does not currently support custom entities declared in the doctype. This is intended to be used for doing minimally intrusive modifications to XML documents, or efficiently extracting content from them.

The [`htmlutil`](https://pkg.go.dev/github.com/pgaskin/htmlstream/htmlutil) package implements utility functions and wrappers for working with HTML content, including some DOM-like helpers for getting node text, sniffing character encoding, parsing attributes, and handling URLs.

The [`polyglot`](https://pkg.go.dev/github.com/pgaskin/htmlstream/polyglot) package converts HTML to polyglot XHTML/HTML4/HTML5 markup, parseable by XML parsers in almost all cases (warning if not). It does its best if the input is malformed, and has an optional strict mode. It emulates most of the HTML5 "adoption agency" algorithm and error recovery, so the final output ends up more or less equivalent to how a browser would parse it. There are hooks to allow a subset of transformations to be made in a single pass. There is also a [CLI version](https://pkg.go.dev/github.com/pgaskin/htmlstream/cmd/polyglottify).

The [`xmlutil`](https://pkg.go.dev/github.com/pgaskin/htmlstream/xmlutil) package provides XML-specific helpers, including XML-style charset detection and declaration parsing. It is compatible with the XML spec for most documents, but does not strictly adhere to it and is generally a bit more lenient (since this library is primarily designed for the HTML spec).

The [`sax`](https://pkg.go.dev/github.com/pgaskin/htmlstream/sax) package provides a simplified SAX-style wrapper for parsing HTML/XHTML/XML. It also provides an [`encoding/xml.TokenReader`](https://pkg.go.dev/encoding/xml#TokenReader) adapter so it can be used with the stdlib [`encoding/xml.Decoder`](https://pkg.go.dev/encoding/xml#Decoder) or other libraries. It supports all `htmlstream` reader modes (universal, html, xml), and behaves accordingly. It also supports reading a partial fragment.

The [`ascii`](https://pkg.go.dev/github.com/pgaskin/htmlstream/ascii) package has ASCII string/bytes helpers, exported since I'm sick of copying and pasting them around.

#### Robustness

This library is optimized for allocations, and, to a slightly lesser degree, throughput. There are zero-copy versions of most methods. It is designed to not crash even on malformed inputs. The `htmlstream`, `polyglot`, and `sax` packages have configurable bounded resource requirements, even for untrusted input.

This library is also designed to pass through malformed input as faithfully as possible (byte-identical in most cases). If content cannot be represented or parsed appropriately, it will emit warnings/errors, which can be handled by the caller. Most of the errors are sticky to reduce the amount of error handling boilerplate.

This library is designed to work on UTF-8 or ASCII-compatible input, but has helpers for sniffing/parsing the charset which can be used with [`x/text/encoding`](https://pkg.go.dev/golang.org/x/text/encoding) to convert it on-the-fly.

This library is well-tested, fuzzed, and used in quite a few of my projects. Although it is relatively new, a lot of the core code and design is based on previous things I've written. The high-level `htmlutil`/`xmlutil`/`sax` packages are a little bit less battle-tested, but should still be stable.

I haven't released a v1 yet since I want to be able to make minor changes to the API, but it can be considered stable enough for production use regardless.

#### LLM Usage

I used Claude to write a lot of the actual code and do the fuzzing, but I designed the API myself, and I reviewed (and tweaked) a lot of the code it wrote (and corrected a few misconceptions it had). If anything, this significantly increased the code quality and robustness since the LLM wrote a lot of fuzzers, and spent more time on the rarer edge cases than I would have (e.g., I wasn't originally planning on matching the HTML5 spec for adopting misnested or unclosed elements, but it spent the time to handle that correctly). I spent almost a week's worth of time iterating upon this library with Claude, even after it finished my original specification. Furthermore, I've already done a lot of other work in this area (kepubify, ebook tooling, multiple scrapers, some private projects), and I had Claude use it as a starting point. Note that unlike my other projects, I let Claude write some of the doc comments and examples given the large API surface (I understand the API and semantics since I designed it, so I'm not really motivated to spend too much time writing extensive docs, but it can be a bit daunting without them).

<!-- TODO: some examples here -->
