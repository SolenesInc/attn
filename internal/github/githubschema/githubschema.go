// Package githubschema checks GraphQL queries against GitHub's public schema,
// refreshed from https://docs.github.com/public/fpt/schema.docs.graphql.
package githubschema

import (
	_ "embed"
	"errors"
	"sync"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

//go:embed schema.docs.graphql
var source string

var load = sync.OnceValues(func() (*ast.Schema, error) {
	return gqlparser.LoadSchema(&ast.Source{Name: "schema.docs.graphql", Input: source})
})

func Validate(query string) error {
	schema, err := load()
	if err != nil {
		return err
	}
	if _, errs := gqlparser.LoadQuery(schema, query); len(errs) > 0 {
		return errors.New(errs.Error())
	}
	return nil
}
