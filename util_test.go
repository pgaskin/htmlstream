package htmlstream

import "testing"

func TestValidXML(t *testing.T) {
	for name, ok := range map[string]bool{"a": true, "a:b": true, "a:b:c": false, ":a": false, "a:": false, "1a": false, "a-1.x": true, "é": true, "a b": false, "": false, "a\u00d7": false, "a\u0713": false, "\u00b7a": false, "a\u00b7": true, "x:\u00e9": true, "a:1": false, "a:-": false, "a:\u00b7": false, "a:b1": true} {
		if ValidXMLName(name) != ok {
			t.Errorf("ValidXMLName(%q) != %v", name, ok)
		}
	}
	if !ValidXMLChars([]byte("a\tb\n\u00e9")) || ValidXMLChars("a\x01") || ValidXMLChars([]byte("\xff")) || ValidXMLChars([]byte("\xed\xa0\x80")) || ValidXMLChars("\uffff") || !ValidXMLChars([]byte("\ufffd\U0010ffff")) {
		t.Errorf("ValidXMLChars")
	}
}

func TestAdjustName(t *testing.T) {
	if got := AdjustSVGAttrName("viewbox") + AdjustSVGTagName("foreignobject") + AdjustSVGTagName("fedropshadow") + AdjustSVGTagName("g"); got != "viewBoxforeignObjectfeDropShadowg" {
		t.Errorf("AdjustSVG*Name: %q", got)
	}
	if got := AdjustMathMLAttrName("definitionurl") + AdjustMathMLAttrName("mathvariant"); got != "definitionURLmathvariant" {
		t.Errorf("AdjustMathMLAttrName: %q", got)
	}
}
