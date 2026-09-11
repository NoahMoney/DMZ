// Package hygiene holds repository-wide checks that no single tool owns. Its tests run with the rest of tools/:
//
//	cd tools && go test ./hygiene
//
// The house rule they enforce is in README.md §11: no machine-specific path in any file.
package hygiene
