// This file marks web/dashboard as a separate (empty) Go module so that
// `go ... ./...` and golangci-lint in the repository root never pick up Go
// files shipped inside node_modules (some npm packages contain them).
module github.com/roshbaikfb/iftahonline/web/dashboard

go 1.27.3
