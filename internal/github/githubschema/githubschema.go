// Package githubschema checks GraphQL queries against GitHub's public schema,
// refreshed from https://docs.github.com/public/fpt/schema.docs.graphql.
package githubschema

import (
	_ "embed"
	"errors"
	"sync"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/validator"
)

//go:embed schema.docs.graphql
var source string

var load = sync.OnceValues(func() (*ast.Schema, error) {
	return gqlparser.LoadSchema(&ast.Source{Name: "schema.docs.graphql", Input: source})
})

func Validate(query string, variables map[string]any) error {
	schema, err := load()
	if err != nil {
		return err
	}
	document, errs := gqlparser.LoadQuery(schema, query)
	if len(errs) > 0 {
		return errors.New(errs.Error())
	}
	for _, operation := range document.Operations {
		if _, err := validator.VariableValues(schema, operation, variables); err != nil {
			return err
		}
	}
	return nil
}
