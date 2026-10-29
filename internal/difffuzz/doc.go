// Package difffuzz tests the polyglot and htmlutil packages against
// golang.org/x/net/html and encoding/xml.
//
// It is a separate module, so that the htmlstream module doesn't depend on
// golang.org/x/net, and has only tests. Run them in this directory:
//
//	go test .
//
// The tests and the seeds of the fuzz targets use the test cases of the
// htmlstream module's testdata/polyglot (expected outputs and warnings, and
// fuzz seeds in seeds.txtar and fuzz.txtar) and the html5lib tree
// construction inputs of the html5lib-tests copy in the golang.org/x/net
// module (html/testdata/html5lib-tests), found with go mod download. Without
// the module (e.g., offline with an empty module cache), the html5lib inputs
// are left out, which -v shows.
//
// # Comparing documents
//
// Documents are compared as DOMs dumped as indented text, ignoring comments,
// doctypes, whitespace-only text, namespace declarations, xml:lang,
// character encoding declarations, and the CDATA wrappers in scripts and
// styles. The x/net and encoding/xml DOMs of an output are compared with
// whitespace-only text, which both must have in the same places. Known differences between x/net and the HTML Standard are worked
// around by rewriting the input before x/net parses it (specDoctypes,
// specPreNewlines, specCaptionTh, xnetTemplateTableSpace, xnetDroppedSlash),
// or the input is skipped (xnetScriptEscape, xnetTextareaCR,
// xnetForeignTemplate). Whitespace after a body or html end tag which closed
// other elements is removed from the input if that makes the DOMs equal
// (dropSpaceAfterBody). The XML output must also meet the namespace
// constraints of Namespaces in XML 1.0, which encoding/xml doesn't check
// (xmlNSCheck).
//
// # Tests
//
// TestPolyglot checks the polyglot.Convert output and warnings for the test
// cases with expected outputs, and compares the x/net DOMs of the input and
// the output, and the encoding/xml and x/net DOMs of the output. It also
// compares them for the output without a doctype (NoDoctype), which is in
// quirks mode, when it has no warnings.
//
// TestDocumentHead checks that htmlutil.Document puts the same elements in
// the head as x/net.
//
// TestPrescanCharsetXNet compares htmlutil.PrescanCharset with the prescan
// of golang.org/x/net/html/charset where they should agree, and checks their
// known differences.
//
// TestXMLLint checks the polyglot.Convert output for the FuzzPolyglotDOM
// seeds with xmllint, if it is installed. The -corpus flag adds directories
// of FuzzPolyglotDOM corpus files, such as the generated corpus in the fuzz
// cache ($(go env GOCACHE)/fuzz/github.com/pgaskin/htmlstream/internal/difffuzz/FuzzPolyglotDOM):
//
//	go test -run '^TestXMLLint$' . -corpus DIR
//
// # Fuzz targets
//
// FuzzPolyglotDOM checks polyglot.Convert: without warnings, x/net parses
// the output to the same DOM as the input, and encoding/xml accepts the
// output and parses it to the same DOM as x/net; with warnings, encoding/xml
// still accepts the output, unless a script of a type other than JavaScript
// or JSON contains "<" or "&". Inputs for which it warns about a table
// closing a p in quirks mode are also converted without a doctype.
//
// FuzzPolyglotWriter does the same checks for polyglot.Writer, with the
// Reader options polyglot.Convert doesn't use (XML syntax, scripting), with
// an element left out or unwrapped, and without a doctype, selected by a
// mode byte.
//
// FuzzPolyglotHooks checks the invariants of the polyglot.Options OnTag and
// OnText hooks (also with holds of the output, which must not change it), the
// output with random edits allowed by their documentation (well-formed XML, and
// matching XML and HTML DOMs without warnings), and the output with random
// renames (the same HTML DOM as without them, with the names in each rename
// group made the same).
//
// FuzzPrescanCharsetXNet compares htmlutil.PrescanCharset with the prescan of
// golang.org/x/net/html/charset, except where they differ in a known way.
//
// Fuzz one target at a time, here limited to 4 CPU cores:
//
//	systemd-run --user --scope -p CPUQuota=400% go test -run '^$' -fuzz '^FuzzPolyglotDOM$' -fuzztime 1m -parallel 4
//
// # Failures
//
// The fuzzer writes a failing input to testdata/fuzz/<target>/<name>. Run
// it again with:
//
//	go test -run '^FuzzPolyglotDOM/<name>$' .
//
// TestProbe prints the polyglot.Convert output and warnings, the DOMs and the
// Reader tokens for the input in a corpus file, or for each line of another
// file (a quoted Go string, or the input as is):
//
//	go test -v -run '^TestProbe$' . -probe testdata/fuzz/FuzzPolyglotDOM/<name>
//
// After the fix, add the input to the htmlstream module's
// testdata/polyglot, as a test case with its expected output or in
// fuzz.txtar, or for the targets with a mode byte, to writerFound or
// hooksFound, and delete the corpus file.
package difffuzz
