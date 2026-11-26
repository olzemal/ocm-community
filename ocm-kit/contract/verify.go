package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Probe evaluates a requirement. Verify checks cancellation around probe calls;
// probes that perform I/O should pass the context to their client operations.
type Probe interface {
	Satisfy(ctx context.Context, req Requirement) (satisfied bool, source string, err error)
}

type RequirementKind string

const (
	Kubernetes RequirementKind = "kubernetes"
	Cluster    RequirementKind = "cluster"
	Node       RequirementKind = "nodes"
	API        RequirementKind = "apis"
	Feature    RequirementKind = "features"
)

type Requirement struct {
	Kind   RequirementKind `json:"kind"`
	Detail fmt.Stringer    `json:"detail"`
}

func (r Requirement) String() string {
	return strings.TrimSpace(fmt.Sprintf("%s %s", r.Kind, r.Detail))
}

type SatisfiedRequirement struct {
	Requirement Requirement `json:"requirement"`
	Source      string      `json:"source"`
}

type ProbeError struct {
	Requirement Requirement `json:"requirement"`
	Err         error       `json:"-"`
}

func (e ProbeError) MarshalJSON() ([]byte, error) {
	message := ""
	if e.Err != nil {
		message = e.Err.Error()
	}
	return json.Marshal(struct {
		Requirement Requirement `json:"requirement"`
		Error       string      `json:"error"`
	}{e.Requirement, message})
}

type Result struct {
	Satisfied  []SatisfiedRequirement `json:"satisfied"`
	Missing    []Requirement          `json:"missing"`
	Unverified []Requirement          `json:"unverified"`
	Errors     []ProbeError           `json:"errors"`
}

func (r Result) String() string {
	var text strings.Builder
	for _, satisfied := range r.Satisfied {
		_, _ = fmt.Fprintf(&text, "[OK] %s (%s)\n", satisfied.Requirement, satisfied.Source)
	}
	for _, missing := range r.Missing {
		_, _ = fmt.Fprintf(&text, "[MISSING] %s\n", missing)
	}
	for _, unverified := range r.Unverified {
		_, _ = fmt.Fprintf(&text, "[UNVERIFIED] %s\n", unverified)
	}
	for _, probeError := range r.Errors {
		_, _ = fmt.Fprintf(&text, "[ERROR] %s: %s\n", probeError.Requirement, probeError.Err)
	}
	_, _ = fmt.Fprintf(&text, "\n%d satisfied, %d missing, %d unverified, %d probe errors.\n", len(r.Satisfied), len(r.Missing), len(r.Unverified), len(r.Errors))

	return text.String()
}

// Verify tries probes in order, retaining only the first satisfaction source.
// Without a satisfier, a clean negative takes precedence over probe errors,
// as specified by the RFC. Errors take precedence over not-applicable results.
func Verify(ctx context.Context, c *ComponentContract, probes ...Probe) (*Result, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if len(probes) == 0 {
		return nil, errors.New("at least one probe is required")
	}
	for _, probe := range probes {
		if probe == nil {
			return nil, errors.New("probe must not be nil")
		}
	}
	result := &Result{
		Satisfied:  []SatisfiedRequirement{},
		Missing:    []Requirement{},
		Unverified: []Requirement{},
		Errors:     []ProbeError{},
	}
	for _, req := range c.requirements() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		satisfied, evaluated := false, false
		var probeErrors []ProbeError
		for _, probe := range probes {
			ok, source, err := probe.Satisfy(ctx, req)
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if errors.Is(err, ErrProbeNotApplicable) {
				continue
			}
			if err != nil {
				probeErrors = append(probeErrors, ProbeError{Requirement: req, Err: err})
				continue
			}
			if ok {
				result.Satisfied = append(result.Satisfied, SatisfiedRequirement{Requirement: req, Source: source})
				satisfied = true
				break
			}
			evaluated = true
		}
		if satisfied {
			continue
		}
		switch {
		case evaluated:
			result.Missing = append(result.Missing, req)
		case len(probeErrors) > 0:
			result.Errors = append(result.Errors, probeErrors...)
		default:
			result.Unverified = append(result.Unverified, req)
		}
	}
	return result, nil
}

func (c *ComponentContract) requirements() []Requirement {
	var requirements []Requirement
	if c.Requires.Kubernetes != nil {
		requirements = append(requirements, Requirement{Kind: Kubernetes, Detail: *c.Requires.Kubernetes})
	}
	if c.Requires.Cluster != nil {
		requirements = append(requirements, Requirement{Kind: Cluster, Detail: *c.Requires.Cluster})
	}
	for _, node := range c.Requires.Nodes {
		if node.Count == 0 {
			node.Count = 1
		}
		requirements = append(requirements, Requirement{Kind: Node, Detail: node})
	}
	for _, api := range c.Requires.APIs {
		requirements = append(requirements, Requirement{Kind: API, Detail: api})
	}
	for _, feature := range c.Requires.Features {
		requirements = append(requirements, Requirement{Kind: Feature, Detail: feature})
	}
	return requirements
}

func detailAs[T any](req Requirement) (T, error) {
	detail, ok := req.Detail.(T)
	if !ok {
		return detail, fmt.Errorf("%s: unexpected requirement detail %T", req.Kind, req.Detail)
	}
	return detail, nil
}
