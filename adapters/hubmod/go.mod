module github.com/efureev/appmod/adapters/hubmod

go 1.25

require (
	github.com/efureev/appmod/v4 v4.0.0
	github.com/efureev/msghub/v3 v3.0.0
)

require github.com/efureev/go-shutdown/v3 v3.0.0 // indirect

replace github.com/efureev/appmod/v4 => ../..
