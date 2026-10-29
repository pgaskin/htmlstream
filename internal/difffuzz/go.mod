module github.com/pgaskin/htmlstream/internal/difffuzz

go 1.26.0

require (
	github.com/pgaskin/htmlstream v0.0.0
	golang.org/x/net v0.60.0
)

require golang.org/x/text v0.42.0 // indirect

replace github.com/pgaskin/htmlstream => ../..
