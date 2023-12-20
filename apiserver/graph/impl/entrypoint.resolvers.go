package impl

// This file will be automatically regenerated based on the schema, any resolver implementations
// will be copied through when generating and any unknown code will be moved to the end.
// Code generated by github.com/99designs/gqlgen version v0.17.40

import (
	"context"

	"github.com/kubeagi/arcadia/apiserver/graph/generated"
)

// Hello is the resolver for the hello field.
func (r *mutationResolver) Hello(ctx context.Context, name string) (string, error) {
	return name, nil
}

// Hello is the resolver for the hello field.
func (r *queryResolver) Hello(ctx context.Context, name string) (string, error) {
	return name, nil
}

// Mutation returns generated.MutationResolver implementation.
func (r *Resolver) Mutation() generated.MutationResolver { return &mutationResolver{r} }

// Query returns generated.QueryResolver implementation.
func (r *Resolver) Query() generated.QueryResolver { return &queryResolver{r} }

type mutationResolver struct{ *Resolver }
type queryResolver struct{ *Resolver }