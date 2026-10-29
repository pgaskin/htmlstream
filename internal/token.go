package internal

// TokenMode returns the mode (an htmlstream.Mode, as set with
// htmlstream.Reader.SetMode) of the Reader which read tok, an
// *htmlstream.Token, and whether tok was read with XML syntax
// (htmlstream.Reader.SetXMLSyntax, or a document read as HTML in Universal
// mode; false for implied end tags). The htmlstream package, which imports
// this one, sets it in an init function.
var TokenMode func(tok any) (mode uint8, xmlSyntax bool)
