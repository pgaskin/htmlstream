package internal

// Elem describes how HTML parsers treat an HTML element.
type Elem uint32

const (
	Void          Elem = 1 << iota // no end tag (see htmlutil.IsVoid)
	RawText                        // RAWTEXT content
	RCDATA                         // RCDATA content
	Script                         // script data content
	Plaintext                      // PLAINTEXT content
	Noscript                       // RAWTEXT content if scripting is enabled
	OmitEnd                        // end tag may be omitted
	Scope                          // limits the default scope
	TableScope                     // limits the table scope
	ClosesP                        // start tag closes an open p element
	Breakout                       // start tag closes foreign content
	Heading                        // h1-h6
	Head                           // allowed in head
	Special                        // in the HTML "special" category
	Formatting                     // formatting element (reopened after misnested end tags by HTML parsers)
	Marker                         // scope marker for the list of active formatting elements
	Block                          // block-level: text before and after it is on separate lines when rendered (see htmlutil.IsBlock)
	NoReconstruct                  // start tag doesn't reconstruct the active formatting elements
	TableContent                   // allowed in a table outside cells (other content is moved before the table)
	ImpliedEnd                     // closed by the "generate implied end tags" steps of HTML parsers
	Hidden                         // contents aren't rendered as text (see htmlutil.IsHidden)
	Preformatted                   // whitespace in the contents is kept when rendered
)

// HTMLElem returns the flags for the HTML element with the lowercase name
// lname, or zero for other names.
func HTMLElem(lname []byte) Elem {
	return htmlElems[string(lname)]
}

var htmlElems = map[string]Elem{
	"a":          Formatting,
	"abbr":       0,
	"acronym":    0,
	"address":    ClosesP | Special | Block | NoReconstruct,
	"applet":     Scope | Special | Marker,
	"area":       Void | Special,
	"article":    ClosesP | Special | Block | NoReconstruct,
	"aside":      ClosesP | Special | Block | NoReconstruct,
	"audio":      Hidden,
	"b":          Breakout | Formatting,
	"base":       Void | Head | Special | NoReconstruct,
	"basefont":   Void | Head | Special | NoReconstruct,
	"bgsound":    Void | Head | Special | NoReconstruct,
	"big":        Breakout | Formatting,
	"blockquote": ClosesP | Breakout | Special | Block | NoReconstruct,
	"body":       OmitEnd | Breakout | Special | Block | NoReconstruct,
	"br":         Void | Breakout | Special,
	"button":     Special,
	"canvas":     Hidden,
	"caption":    OmitEnd | Scope | Special | Marker | Block | NoReconstruct | TableContent,
	"center":     ClosesP | Breakout | Special | Block | NoReconstruct,
	"code":       Breakout | Formatting,
	"col":        Void | Special | NoReconstruct | TableContent,
	"colgroup":   OmitEnd | Special | NoReconstruct | TableContent,
	"datalist":   Hidden,
	"dd":         OmitEnd | ClosesP | Breakout | Special | Block | NoReconstruct | ImpliedEnd,
	"details":    ClosesP | Special | Block | NoReconstruct,
	"dialog":     ClosesP | Block | NoReconstruct,
	"dir":        ClosesP | Special | Block | NoReconstruct,
	"div":        ClosesP | Breakout | Special | Block | NoReconstruct,
	"dl":         ClosesP | Breakout | Special | Block | NoReconstruct,
	"dt":         OmitEnd | ClosesP | Breakout | Special | Block | NoReconstruct | ImpliedEnd,
	"em":         Breakout | Formatting,
	"embed":      Void | Breakout | Special,
	"fieldset":   ClosesP | Special | Block | NoReconstruct,
	"figcaption": ClosesP | Special | Block | NoReconstruct,
	"figure":     ClosesP | Special | Block | NoReconstruct,
	"font":       Formatting,
	"footer":     ClosesP | Special | Block | NoReconstruct,
	"form":       ClosesP | Special | Block | NoReconstruct | TableContent,
	"frame":      Void | Special | NoReconstruct,
	"frameset":   Special | NoReconstruct,
	"h1":         ClosesP | Breakout | Heading | Special | Block | NoReconstruct,
	"h2":         ClosesP | Breakout | Heading | Special | Block | NoReconstruct,
	"h3":         ClosesP | Breakout | Heading | Special | Block | NoReconstruct,
	"h4":         ClosesP | Breakout | Heading | Special | Block | NoReconstruct,
	"h5":         ClosesP | Breakout | Heading | Special | Block | NoReconstruct,
	"h6":         ClosesP | Breakout | Heading | Special | Block | NoReconstruct,
	"head":       OmitEnd | Breakout | Special | NoReconstruct | Hidden,
	"header":     ClosesP | Special | Block | NoReconstruct,
	"hgroup":     ClosesP | Special | Block | NoReconstruct,
	"hr":         Void | ClosesP | Breakout | Special | Block | NoReconstruct,
	"html":       OmitEnd | Scope | TableScope | Special | Block | NoReconstruct,
	"i":          Breakout | Formatting,
	"iframe":     RawText | Special | NoReconstruct | Hidden,
	"image":      Void,
	"img":        Void | Breakout | Special,
	"input":      Void | Special | TableContent,
	"isindex":    0,
	"keygen":     Void | Special,
	"legend":     Block,
	"li":         OmitEnd | ClosesP | Breakout | Special | Block | NoReconstruct | ImpliedEnd,
	"link":       Void | Head | Special | NoReconstruct,
	"listing":    ClosesP | Breakout | Special | Block | NoReconstruct | Preformatted,
	"main":       ClosesP | Special | Block | NoReconstruct,
	"marquee":    Scope | Special | Marker,
	"menu":       ClosesP | Breakout | Special | Block | NoReconstruct,
	"meta":       Void | Breakout | Head | Special | NoReconstruct,
	"meter":      Hidden,
	"nav":        ClosesP | Special | Block | NoReconstruct,
	"nobr":       Breakout | Formatting,
	"noembed":    RawText | Special | NoReconstruct | Hidden,
	"noframes":   RawText | Head | Special | NoReconstruct | Hidden,
	"noscript":   Noscript | Head | Special | NoReconstruct | Hidden,
	"object":     Scope | Special | Marker,
	"ol":         ClosesP | Breakout | Special | Block | NoReconstruct,
	"optgroup":   OmitEnd | Block | ImpliedEnd,
	"option":     OmitEnd | Block | ImpliedEnd,
	"p":          OmitEnd | ClosesP | Breakout | Special | Block | NoReconstruct | ImpliedEnd,
	"param":      Void | Special | NoReconstruct,
	"plaintext":  Plaintext | ClosesP | Special | Block | NoReconstruct | Preformatted,
	"pre":        ClosesP | Breakout | Special | Block | NoReconstruct | Preformatted,
	"progress":   Hidden,
	"rb":         OmitEnd | ImpliedEnd | NoReconstruct,
	"rp":         OmitEnd | ImpliedEnd | NoReconstruct | Hidden,
	"rt":         OmitEnd | ImpliedEnd | NoReconstruct,
	"rtc":        OmitEnd | ImpliedEnd | NoReconstruct,
	"ruby":       Breakout,
	"s":          Breakout | Formatting,
	"script":     Script | Head | Special | NoReconstruct | TableContent | Hidden,
	"search":     ClosesP | Special | Block | NoReconstruct,
	"section":    ClosesP | Special | Block | NoReconstruct,
	"select":     Scope | Special,
	"small":      Breakout | Formatting,
	"source":     Void | Special | NoReconstruct,
	"span":       Breakout,
	"strike":     Breakout | Formatting,
	"strong":     Breakout | Formatting,
	"style":      RawText | Head | Special | NoReconstruct | TableContent | Hidden,
	"sub":        Breakout,
	"summary":    ClosesP | Special | Block | NoReconstruct,
	"sup":        Breakout,
	"table":      Scope | TableScope | ClosesP | Breakout | Special | Block | NoReconstruct,
	"tbody":      OmitEnd | Special | Block | NoReconstruct | TableContent,
	"td":         OmitEnd | Scope | Special | Marker | NoReconstruct | TableContent,
	"template":   Scope | TableScope | Head | Special | Marker | NoReconstruct | TableContent | Hidden,
	"textarea":   RCDATA | Special | NoReconstruct | Hidden | Preformatted,
	"tfoot":      OmitEnd | Special | Block | NoReconstruct | TableContent,
	"th":         OmitEnd | Scope | Special | Marker | NoReconstruct | TableContent,
	"thead":      OmitEnd | Special | Block | NoReconstruct | TableContent,
	"title":      RCDATA | Head | Special | NoReconstruct | Hidden,
	"tr":         OmitEnd | Special | Block | NoReconstruct | TableContent,
	"track":      Void | Special | NoReconstruct,
	"tt":         Breakout | Formatting,
	"u":          Breakout | Formatting,
	"ul":         ClosesP | Breakout | Special | Block | NoReconstruct,
	"var":        Breakout,
	"video":      Hidden,
	"wbr":        Void | Special,
	"xmp":        RawText | ClosesP | Special | Block | Preformatted,
}

// SVGTagNames and SVGAttrNames map lowercase SVG names to the case HTML
// parsers give them, and MathMLAttrNames does the same for MathML attributes.
// They are the tables of the HTML Standard (see htmlstream.AdjustSVGTagName
// and AdjustSVGAttrName). golang.org/x/net/html leaves out feDropShadow.
var SVGTagNames = map[string]string{
	"altglyph": "altGlyph", "altglyphdef": "altGlyphDef", "altglyphitem": "altGlyphItem",
	"animatecolor": "animateColor", "animatemotion": "animateMotion",
	"animatetransform": "animateTransform", "clippath": "clipPath", "feblend": "feBlend",
	"fecolormatrix": "feColorMatrix", "fecomponenttransfer": "feComponentTransfer",
	"fecomposite": "feComposite", "feconvolvematrix": "feConvolveMatrix",
	"fediffuselighting": "feDiffuseLighting", "fedisplacementmap": "feDisplacementMap",
	"fedistantlight": "feDistantLight", "fedropshadow": "feDropShadow", "feflood": "feFlood", "fefunca": "feFuncA",
	"fefuncb": "feFuncB", "fefuncg": "feFuncG", "fefuncr": "feFuncR",
	"fegaussianblur": "feGaussianBlur", "feimage": "feImage", "femerge": "feMerge",
	"femergenode": "feMergeNode", "femorphology": "feMorphology", "feoffset": "feOffset",
	"fepointlight": "fePointLight", "fespecularlighting": "feSpecularLighting",
	"fespotlight": "feSpotLight", "fetile": "feTile", "feturbulence": "feTurbulence",
	"foreignobject": "foreignObject", "glyphref": "glyphRef",
	"lineargradient": "linearGradient", "radialgradient": "radialGradient",
	"textpath": "textPath",
}

var SVGAttrNames = map[string]string{
	"attributename": "attributeName", "attributetype": "attributeType",
	"basefrequency": "baseFrequency", "baseprofile": "baseProfile", "calcmode": "calcMode",
	"clippathunits": "clipPathUnits", "diffuseconstant": "diffuseConstant",
	"edgemode": "edgeMode", "filterunits": "filterUnits", "glyphref": "glyphRef",
	"gradienttransform": "gradientTransform", "gradientunits": "gradientUnits",
	"kernelmatrix": "kernelMatrix", "kernelunitlength": "kernelUnitLength",
	"keypoints": "keyPoints", "keysplines": "keySplines", "keytimes": "keyTimes",
	"lengthadjust": "lengthAdjust", "limitingconeangle": "limitingConeAngle",
	"markerheight": "markerHeight", "markerunits": "markerUnits", "markerwidth": "markerWidth",
	"maskcontentunits": "maskContentUnits", "maskunits": "maskUnits", "numoctaves": "numOctaves",
	"pathlength": "pathLength", "patterncontentunits": "patternContentUnits",
	"patterntransform": "patternTransform", "patternunits": "patternUnits",
	"pointsatx": "pointsAtX", "pointsaty": "pointsAtY", "pointsatz": "pointsAtZ",
	"preservealpha": "preserveAlpha", "preserveaspectratio": "preserveAspectRatio",
	"primitiveunits": "primitiveUnits", "refx": "refX", "refy": "refY",
	"repeatcount": "repeatCount", "repeatdur": "repeatDur",
	"requiredextensions": "requiredExtensions", "requiredfeatures": "requiredFeatures",
	"specularconstant": "specularConstant", "specularexponent": "specularExponent",
	"spreadmethod": "spreadMethod", "startoffset": "startOffset", "stddeviation": "stdDeviation",
	"stitchtiles": "stitchTiles", "surfacescale": "surfaceScale",
	"systemlanguage": "systemLanguage", "tablevalues": "tableValues", "targetx": "targetX",
	"targety": "targetY", "textlength": "textLength", "viewbox": "viewBox",
	"viewtarget": "viewTarget", "xchannelselector": "xChannelSelector",
	"ychannelselector": "yChannelSelector", "zoomandpan": "zoomAndPan",
}

var MathMLAttrNames = map[string]string{
	"definitionurl": "definitionURL",
}

// SVGURLElems lists the SVG elements with an href (or xlink:href) attribute
// loaded or followed by browsers, in the case used by SVG.
var SVGURLElems = []string{"a", "feImage", "image", "script", "use"}

// URLAttrs lists the attributes of HTML elements whose values are URLs (or
// srcset lists of URLs), by lowercase element name.
var URLAttrs = map[string][]string{
	"a":          {"href"},
	"area":       {"href"},
	"audio":      {"src"},
	"base":       {"href"},
	"blockquote": {"cite"},
	"body":       {"background"},
	"button":     {"formaction"},
	"del":        {"cite"},
	"embed":      {"src"},
	"form":       {"action"},
	"frame":      {"src", "longdesc"},
	"iframe":     {"src", "longdesc"},
	"img":        {"src", "srcset", "longdesc"},
	"input":      {"src", "formaction"},
	"ins":        {"cite"},
	"link":       {"href", "imagesrcset"},
	"object":     {"data"},
	"q":          {"cite"},
	"script":     {"src"},
	"source":     {"src", "srcset"},
	"table":      {"background"},
	"tbody":      {"background"},
	"td":         {"background"},
	"tfoot":      {"background"},
	"th":         {"background"},
	"thead":      {"background"},
	"tr":         {"background"},
	"track":      {"src"},
	"video":      {"src", "poster"},
}
