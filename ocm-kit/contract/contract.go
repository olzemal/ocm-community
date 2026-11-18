// Package contract discovers and evaluates declarative OCM component contracts.
package contract

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	yamlv3 "go.yaml.in/yaml/v3"
	descriptor "ocm.software/open-component-model/bindings/go/descriptor/runtime"
	"sigs.k8s.io/yaml"

	"github.com/open-component-model/community/ocm-kit/compver"
)

var (
	ErrNotFound              = errors.New("component contract resource not found")
	ErrUnsupportedAPIVersion = errors.New("unsupported component contract apiVersion")
	ErrProbeNotApplicable    = errors.New("probe does not evaluate this requirement kind")
)

// Parse accepts exactly one YAML document and rejects unknown or duplicate fields.
func Parse(data []byte) (*ComponentContract, error) {
	// Check the document count separately: UnmarshalStrict reads only one
	// document. Decode a node here so field names come solely from JSON tags below.
	decoder := yamlv3.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(new(yamlv3.Node)); err != nil {
		return nil, fmt.Errorf("contract YAML: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("contract YAML: expected a single document")
	}
	var c ComponentContract
	if err := yaml.UnmarshalStrict(data, &c); err != nil {
		return nil, fmt.Errorf("contract YAML: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func FindContract(desc *descriptor.Descriptor) (*descriptor.Resource, error) {
	if desc == nil {
		return nil, errors.New("component descriptor must not be nil")
	}
	var found *descriptor.Resource
	var unsupported error
	for i := range desc.Component.Resources {
		res := &desc.Component.Resources[i]
		for _, label := range res.Labels {
			if label.Name != LabelName {
				continue
			}
			var version string
			if err := label.GetValue(&version); err != nil {
				return nil, fmt.Errorf("resource %q contract label: %w", res.Name, err)
			}
			if version != "v1" {
				unsupported = fmt.Errorf("%w: resource %q label is %q, support v1", ErrUnsupportedAPIVersion, res.Name, version)
				continue
			}
			if found != nil {
				return nil, errors.New("component has multiple v1 contract resources")
			}
			found = res
		}
	}
	if found != nil {
		return found, nil
	}
	if unsupported != nil {
		return nil, unsupported
	}
	return nil, ErrNotFound
}

func FetchContract(ctx context.Context, repo compver.Repository, desc *descriptor.Descriptor, res *descriptor.Resource) (*ComponentContract, error) {
	if repo == nil || desc == nil || res == nil {
		return nil, errors.New("repository, descriptor and resource must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// ResourceBytes selects by name, so do not allow it to fetch an ambiguous resource.
	matches := 0
	for _, candidate := range desc.Component.Resources {
		if candidate.Name == res.Name {
			matches++
		}
	}
	if matches != 1 {
		return nil, fmt.Errorf("expected one resource named %q, found %d", res.Name, matches)
	}
	data, err := repo.ResourceBytes(ctx, desc.Component.Name, desc.Component.Version, res.Name)
	if err != nil {
		return nil, fmt.Errorf("fetch contract resource %q: %w", res.Name, err)
	}
	return Parse(data)
}

func GetContract(ctx context.Context, repo compver.Repository, desc *descriptor.Descriptor) (*ComponentContract, error) {
	res, err := FindContract(desc)
	if err != nil {
		return nil, err
	}
	return FetchContract(ctx, repo, desc, res)
}
