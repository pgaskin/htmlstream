# htmlstream

[![Go Reference](https://pkg.go.dev/badge/github.com/pgaskin/htmlstream.svg)](https://pkg.go.dev/github.com/pgaskin/htmlstream)
[![Test](https://github.com/pgaskin/htmlstream/actions/workflows/test.yml/badge.svg)](https://github.com/pgaskin/htmlstream/actions/workflows/test.yml)
[![Fuzz](https://github.com/pgaskin/htmlstream/actions/workflows/fuzz.yml/badge.svg)](https://github.com/pgaskin/htmlstream/actions/workflows/fuzz.yml)

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

The [`polyglot`](https://pkg.go.dev/github.com/pgaskin/htmlstream/polyglot) package converts HTML to polyglot XHTML/HTML4/HTML5 markup, parseable by XML parsers in almost all cases (warning if not). It does its best if the input is malformed, and has an optional strict mode. It emulates most of the HTML5 "adoption agency" algorithm and error recovery, so the final output ends up more or less equivalent to how a browser would parse it. There are hooks to allow a subset of transformations to be made in a single pass. There is also a [CLI version](https://pkg.go.dev/github.com/pgaskin/htmlstream/cmd/polyglottify) and a [web version](https://pgaskin.net/htmlstream/polyglottify/).

The [`xmlutil`](https://pkg.go.dev/github.com/pgaskin/htmlstream/xmlutil) package provides XML-specific helpers, including XML-style charset detection and declaration parsing. It is compatible with the XML spec for most documents, but does not strictly adhere to it and is generally a bit more lenient (since this library is primarily designed for the HTML spec).

The [`sax`](https://pkg.go.dev/github.com/pgaskin/htmlstream/sax) package provides a simplified SAX-style wrapper for parsing HTML/XHTML/XML. It also provides an [`encoding/xml.TokenReader`](https://pkg.go.dev/encoding/xml#TokenReader) adapter so it can be used with the stdlib [`encoding/xml.Decoder`](https://pkg.go.dev/encoding/xml#Decoder) or other libraries. It supports all `htmlstream` reader modes (universal, html, xml), and behaves accordingly. It also supports reading a partial fragment.

The [`ascii`](https://pkg.go.dev/github.com/pgaskin/htmlstream/ascii) package has ASCII string/bytes helpers, exported since I'm sick of copying and pasting them around.

#### Robustness

This library is optimized for allocations, and, to a slightly lesser degree, throughput. There are zero-copy versions of most methods. It is designed to not crash even on malformed inputs. The `htmlstream`, `polyglot`, and `sax` packages have configurable bounded resource requirements, even for untrusted input (these should be configured explicitly and tuned to your use case if you depend on it).

This library is also designed to pass through malformed input as faithfully as possible (byte-identical in most cases). If content cannot be represented or parsed appropriately, it will emit warnings/errors, which can be handled by the caller. Most of the errors are sticky to reduce the amount of error handling boilerplate.

This library is designed to work on UTF-8 or ASCII-compatible input, but has helpers for sniffing/parsing the charset which can be used with [`x/text/encoding`](https://pkg.go.dev/golang.org/x/text/encoding) to convert it on-the-fly.

The polyglot output is intended to be idempotent in almost all cases, and this is checked for the entire test suite. This is not a guarantee in 100% of cases (it's quite hard to prove it), but I will fix it if I ever find a case where it isn't.

The polyglot output also tries to preserve the original document's structure as much as possible, including for mostly-cosmetic things like whitespace placement.

This library is well-tested, fuzzed, and used in quite a few of my projects. Although it is relatively new, a lot of the core code and design is based on previous things I've written. The high-level `htmlutil`/`xmlutil`/`sax` packages are a little bit less battle-tested, but should still be stable.

I haven't released a v1 yet since I want to be able to make minor changes to the API, but it can be considered stable enough for production use regardless.

#### LLM Usage

This README is written entirely by me.

I designed the API and architected the internal bits of this library almost entirely by hand. Claude did the implementation, using code I had previously written for kepubify and other projects over the last 10 years a starting point. I read ALL of the thinking traces, and actively worked with claude on figuring out the details. I spent around 60 hours in total (over multiple days) working on this library, with more than half of that time being after Claude thought it was complete.

I let Claude write the initial set of doc comments and examples given the large API surface, and verified them for accuracy (but not concision and clarity yet, see below). In the past, I would have left the initial v0 release of a library like this with minimal documentation, as I already understand it since I wrote it.

All commits are closely reviewed by me, and I write ALL commit messages myself, including the ones `Co-Authored-By` claude (for those, I also have it read my hand-written commit message to ensure I understand the change and its implications correctly). I do this (unlike with some of my other LLM-assisted projects like [Vento](https://github.com/pgaskin/vento)) since a lot of the changes here are quite subtle, and I also need to ensure behaviour changes are documented correctly.

For the same reason, I'm currently working on rewriting all the doc comments and examples by hand. They're all accurate regardless (I've reviewed them myself), but most are overly verbose (burying important details), and some emphasize the wrong details or underspecify some of the behaviour I already guarantee. I figured overly-verbose documentation is better than none for the initial release at any rate (and it also helps LLMs use this library correctly).

On a more positive note, the amount of fuzzing Claude was able to do (both by reasoning about the code adversially, and by writing comprehensive fuzzers) significantly improved the quality and robustness of this library past what I'd be able to do on my own, and has made me much more confident in the output.

Without the LLM-assisted fuzzing and testing, I would have needed to drop some of the trickier/rarer features like quirks mode support, polyglot output idempotency, and matching the HTML5 spec and test suite (rather than just approximating the common parts), the SAX wrapper, and the stdlib xml adapter, as it'd be too hard to guarantee correct handling without formal verification. In addition, there would probably be a lot more unhandled edge-cases, as the specifications aren't explicit about quite a few of them, and the publicly available test data doesn't cover all of them.

And, without LLMs, I probably would never have gotten around to creating this library, even though I had wanted to for years, since the benefit wouldn't have outweighed the time cost versus doing more specific one-off things for projects where I needed it.

<!-- TODO: some examples here -->
