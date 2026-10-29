// Package xmltest_testdata provides James Clark's XML test cases, version
// 1998-11-18, from ftp://ftp.jclark.com/pub/xml/xmltest.zip, read from
// testdata/xmltest/xmltest.zip.
//
// Their license (xmltest/readme.html in the zip) only allows redistributing
// the zip unmodified:
//
//	Copyright (C) 1998 James Clark.  All rights reserved.  Permission is
//	granted to copy and modify this collection in any way for internal use
//	within a company or organization.  Permission is granted to
//	redistribute the file xmltest.zip containing this collection to third
//	parties provided that no modifications of any kind are made to this
//	file.  Note that permission to distribute the collection in any other
//	form is not granted.
package xmltest_testdata

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"iter"
	"maps"
	"path"
	"slices"
	"strings"
)

//go:embed xmltest.zip
var zipData []byte

var (
	files = map[string]string{} // by name
	names []string              // the names of files, sorted
	docs  []Document            // sorted by name
)

func init() {
	z, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		panic(err)
	}
	for _, f := range z.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			panic(err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			panic(fmt.Errorf("%s: %w", f.Name, err))
		}
		files[f.Name] = string(b)
	}
	names = slices.Sorted(maps.Keys(files))
	for _, name := range names {
		dir, base := path.Split(name)
		if path.Ext(base) != ".xml" {
			continue
		}
		d := Document{Name: name, Data: files[name]}
		switch f := strings.Split(strings.TrimSuffix(dir, "/"), "/"); {
		case len(f) == 3 && f[0] == "xmltest" && (f[1] == "valid" || f[1] == "not-wf") && (f[2] == "sa" || f[2] == "ext-sa" || f[2] == "not-sa"):
			d.Kind, d.Group = f[1], f[2]
		case len(f) == 2 && f[0] == "xmltest" && f[1] == "invalid":
			d.Kind = f[1]
		case len(f) == 4 && f[0] == "xmltest" && f[1] == "valid" && f[3] == "out":
			continue // canonical forms
		default:
			panic("unexpected file " + name)
		}
		if d.Kind == "valid" {
			var ok bool
			if d.Canonical, ok = files[dir+"out/"+base]; !ok {
				panic("no canonical form for " + name)
			}
		}
		docs = append(docs, d)
	}
	if len(docs) == 0 {
		panic("no documents")
	}
}

// Document is a test document.
type Document struct {
	Name string // the name of the file in the zip, e.g. "xmltest/valid/sa/001.xml"
	Data string // the contents of the file

	// Kind is "valid" (valid documents), "invalid" (well-formed documents
	// which aren't valid) or "not-wf" (documents which aren't
	// well-formed).
	Kind string

	// Group is "sa" (standalone documents without references to external
	// general entities), "ext-sa" (standalone documents with references to
	// external general entities) or "not-sa" (documents which aren't
	// standalone), or empty for invalid documents.
	Group string

	// Canonical is the equivalent document in James Clark's canonical XML
	// form (see xmltest/canonxml.html in the zip) for valid documents, or
	// empty for others.
	Canonical string
}

// Resolve returns the contents of the external entity or DTD with the
// system identifier systemID (a path relative to the directory of the
// document, e.g. "001.ent"), if it is in the zip.
func (d Document) Resolve(systemID string) (string, bool) {
	return File(path.Join(path.Dir(d.Name), systemID))
}

// Documents returns the test documents (the .xml files outside the out
// directories), sorted by name.
func Documents() iter.Seq[Document] {
	return slices.Values(docs)
}

// Files returns the names and contents of the files in the zip (test
// documents, external entities, canonical forms and the documentation),
// sorted by name.
func Files() iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		for _, name := range names {
			if !yield(name, files[name]) {
				return
			}
		}
	}
}

// File returns the contents of the named file in the zip, e.g.
// "xmltest/valid/sa/001.ent", if present.
func File(name string) (string, bool) {
	s, ok := files[name]
	return s, ok
}
