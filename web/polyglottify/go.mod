module polyglottify-web

go 1.26.10

require (
	github.com/evanw/esbuild v0.28.2
	github.com/pgaskin/go-hbsubset v0.0.2
	github.com/pgaskin/go-woff2 v0.0.3
	github.com/pgaskin/htmlstream v0.0.0
)

require golang.org/x/sys v0.49.0 // indirect

replace github.com/pgaskin/htmlstream => ../../
