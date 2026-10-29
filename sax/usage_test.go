package sax_test

import (
	"encoding/xml"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/pgaskin/htmlstream"
	"github.com/pgaskin/htmlstream/htmlutil"
	"github.com/pgaskin/htmlstream/sax"
)

type opfPackage struct {
	Title, Creator, Modified string
	Manifest                 []opfItem
	Spine                    []string
}

type opfItem struct{ ID, Href, MediaType, Properties string }

// parseOPF reads an EPUB package document with the pull API.
func parseOPF(in string) (*opfPackage, error) {
	r := htmlstream.NewReader(strings.NewReader(in))
	r.SetMode(htmlstream.XML)
	s := sax.NewReader(r)
	p := new(opfPackage)
	text := func() string {
		b, _ := s.ReadText(nil, 4096)
		return string(b)
	}
	for ev := range s.All() {
		if ev.Kind != sax.StartElement {
			continue
		}
		switch ev.Name {
		case sax.Name{Space: nsDC, Local: "title"}:
			p.Title = text()
		case sax.Name{Space: nsDC, Local: "creator"}:
			p.Creator = text()
		case sax.Name{Space: nsOPF, Local: "meta"}:
			if v, _ := ev.Attr("property"); v == "dcterms:modified" {
				p.Modified = text()
			}
		case sax.Name{Space: nsOPF, Local: "item"}:
			if path := s.Path(); path[len(path)-2] != (sax.Name{Space: nsOPF, Local: "manifest"}) {
				break
			}
			var it opfItem
			it.ID, _ = ev.Attr("id")
			it.Href, _ = ev.Attr("href")
			it.MediaType, _ = ev.Attr("media-type")
			it.Properties, _ = ev.Attr("properties")
			p.Manifest = append(p.Manifest, it)
		case sax.Name{Space: nsOPF, Local: "itemref"}:
			v, _ := ev.Attr("idref")
			p.Spine = append(p.Spine, v)
		}
	}
	return p, s.Err()
}

func TestOPF(t *testing.T) {
	p, err := parseOPF(opf)
	want := &opfPackage{
		Title:    "Pride & Prejudice — Vol. 1",
		Creator:  "Jane Austen & Co.",
		Modified: "2020-01-01T00:00:00Z",
		Manifest: []opfItem{
			{"nav", "nav.xhtml", "application/xhtml+xml", "nav"},
			{"c1", "c1.xhtml", "application/xhtml+xml", ""},
			{"css", "style.css", "text/css", ""},
		},
		Spine: []string{"c1"},
	}
	if err != nil || fmt.Sprint(p) != fmt.Sprint(want) {
		t.Errorf("pull: %+v, %v", p, err)
	}

	var v struct {
		Metadata struct {
			Title    string `xml:"http://purl.org/dc/elements/1.1/ title"`
			Creator  string `xml:"http://purl.org/dc/elements/1.1/ creator"`
			Modified string `xml:"http://www.idpf.org/2007/opf meta"`
		} `xml:"http://www.idpf.org/2007/opf metadata"`
	}
	if err := xml.Unmarshal([]byte(opf), &v); err == nil {
		t.Errorf("encoding/xml reads the malformed document")
	}
	r := htmlstream.NewReader(strings.NewReader(opf))
	r.SetMode(htmlstream.XML)
	if err := xml.NewTokenDecoder(sax.NewTokenReader(sax.NewReader(r))).Decode(&v); err != nil || v.Metadata.Title != want.Title || v.Metadata.Creator != want.Creator || v.Metadata.Modified != want.Modified {
		t.Errorf("TokenReader: %+v, %v", v, err)
	}
}

// tableRows reads the rows of the tables of an HTML document with the pull
// API: the table part kinds are kept per open element in a slice indexed by
// depth, since every StartElement event has an EndElement event.

type tableCell struct {
	Text   string
	Header bool
}

type tableRow struct {
	Section string
	Cells   []tableCell
}

type partKind uint8

const (
	partNone partKind = iota // not a table part, or inside a cell
	partTable
	partSection
	partRow
	partCell
	partCaption
	partOther
)

type tableState struct {
	section string
	inRow   bool
	row     []tableCell
	header  bool
	text    htmlutil.TextBuilder
}

type tablePart struct {
	kind partKind
	t    *tableState
}

func tableRows(in string, yield func(tableRow)) error {
	s := sax.NewReader(htmlstream.NewReader(strings.NewReader(in)))
	var parts []tablePart // the part of each open element
	top := func() tablePart {
		for i := len(parts) - 1; i >= 0; i-- {
			if parts[i].kind != partNone {
				return parts[i]
			}
		}
		return tablePart{}
	}
	endRow := func(ts *tableState) {
		if ts.inRow {
			ts.inRow = false
			yield(tableRow{ts.section, ts.row})
			ts.row = nil
		}
	}
	xhtml := func(local string) sax.Name { return sax.Name{Space: htmlstream.NamespaceXHTML, Local: local} }
	for ev := range s.All() {
		p := top()
		inText := p.kind == partCell || p.kind == partCaption
		switch ev.Kind {
		case sax.Text:
			if inText {
				p.t.text.Write(ev.Text)
			}
		case sax.StartElement:
			np := tablePart{}
			switch {
			case ev.Name == xhtml("table"):
				if p.t != nil {
					p.t.text.Break()
				}
				np = tablePart{partTable, &tableState{section: "tbody"}}
			case inText:
				if htmlutil.IsHidden(ev) {
					s.Skip() // the loop doesn't see its EndElement event
					continue
				} else if htmlutil.IsTextBreak(ev) {
					p.t.text.Break()
				}
			case p.kind != partNone:
				ts := p.t
				np = tablePart{partOther, ts}
				switch n := ev.Name; {
				case n == xhtml("caption") && p.kind == partTable:
					np.kind = partCaption
					ts.text.Reset()
				case (n == xhtml("thead") || n == xhtml("tbody") || n == xhtml("tfoot")) && p.kind == partTable:
					endRow(ts)
					np.kind, ts.section = partSection, n.Local
				case n == xhtml("tr") && (p.kind == partTable || p.kind == partSection):
					endRow(ts)
					np.kind, ts.inRow = partRow, true
				case (n == xhtml("td") || n == xhtml("th")) && p.kind != partOther && p.kind != partCell:
					np.kind, ts.inRow, ts.header = partCell, true, n.Local == "th"
					ts.text.Reset()
				}
			}
			parts = append(parts, np)
		case sax.EndElement:
			ep := parts[len(parts)-1]
			parts = parts[:len(parts)-1]
			if ep.kind == partNone {
				if inText && htmlutil.IsTextBreak(ev) {
					p.t.text.Break()
				}
				continue
			}
			ts := ep.t
			switch ep.kind {
			case partCell:
				ts.row = append(ts.row, tableCell{ts.text.String(), ts.header})
			case partRow, partSection, partTable:
				endRow(ts)
				if ep.kind == partSection {
					ts.section = "tbody"
				}
				if t := top(); ep.kind == partTable && t.t != nil {
					t.t.text.Break()
				}
			}
		}
	}
	return s.Err()
}

func TestTables(t *testing.T) {
	const in = `<table><caption>c</caption><thead><tr><th>h1<th>h2<tbody><tr><td>a<br>b<td>x<script>no</script>y<table><td>inner</table>z<tr><td>last</table>`
	var rows []string
	err := tableRows(in, func(r tableRow) { rows = append(rows, fmt.Sprint(r)) })
	want := []string{
		"{thead [{h1 true} {h2 true}]}",
		"{tbody [{inner false}]}",
		"{tbody [{a b false} {xy z false}]}",
		"{tbody [{last false}]}",
	}
	if err != nil || !slices.Equal(rows, want) {
		t.Errorf("rows\n%s\nwant\n%s\nerror %v", strings.Join(rows, "\n"), strings.Join(want, "\n"), err)
	}
}
