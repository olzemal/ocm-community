package contract

import (
	"context"
	"fmt"
	"slices"
)

// Provider associates a component reference with its contract.
type Provider struct {
	Ref      string             `json:"ref"`
	Contract *ComponentContract `json:"contract"`
}

// CatalogProbe matches API and feature requirements against component claims.
// It does not evaluate Kubernetes version or capacity requirements or inspect a cluster.
type CatalogProbe struct {
	providers []Provider
}

// NewCatalogProbe validates the provider contracts before creating the probe.
func NewCatalogProbe(providers []Provider) (*CatalogProbe, error) {
	for _, provider := range providers {
		if provider.Ref == "" {
			return nil, fmt.Errorf("catalog provider reference must not be empty")
		}
		if err := provider.Contract.Validate(); err != nil {
			return nil, fmt.Errorf("provider %q: %w", provider.Ref, err)
		}
	}
	return &CatalogProbe{providers: slices.Clone(providers)}, nil
}

func (p *CatalogProbe) Satisfy(ctx context.Context, req Requirement) (bool, string, error) {
	if err := ctx.Err(); err != nil {
		return false, "", err
	}
	switch req.Kind {
	case API:
		api, err := detailAs[APIRequirement](req)
		if err != nil {
			return false, "", err
		}
		return p.satisfyAPIRequirement(api)
	case Feature:
		feature, err := detailAs[FeatureRequirement](req)
		if err != nil {
			return false, "", err
		}
		return p.satisfyFeatureRequirement(feature)
	default:
		return false, "", ErrProbeNotApplicable
	}
}

func (p *CatalogProbe) satisfyFeatureRequirement(feature FeatureRequirement) (bool, string, error) {
	for _, provider := range p.providers {
		for _, declaration := range provider.Contract.Provides.Features {
			if declaration.Capability == feature.Capability {
				return true, "component:" + provider.Ref, nil
			}
		}
	}
	return false, "", nil
}

func (p *CatalogProbe) satisfyAPIRequirement(api APIRequirement) (bool, string, error) {
	for _, provider := range p.providers {
		for _, declaration := range provider.Contract.Provides.APIs {
			if declaration.Group != api.Group || declaration.Kind != api.Kind {
				continue
			}
			for _, version := range api.Versions {
				if slices.Contains(declaration.Versions, version) {
					return true, "component:" + provider.Ref, nil
				}
			}
		}
	}
	return false, "", nil
}
