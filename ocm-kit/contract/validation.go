package contract

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/go-playground/validator/v10"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation"
)

// Configure once before use; the validator caches struct metadata and is safe
// for concurrent validation after registration.
var contractValidator = newContractValidator()

func newContractValidator() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	v.RegisterTagNameFunc(func(field reflect.StructField) string {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		return name
	})
	for name, rule := range map[string]validator.Func{
		"notblank": func(fl validator.FieldLevel) bool {
			return strings.TrimSpace(fl.Field().String()) != ""
		},
		"semver_constraint": func(fl validator.FieldLevel) bool {
			_, err := semver.NewConstraint(fl.Field().String())
			return err == nil
		},
		"positive_quantity": func(fl validator.FieldLevel) bool {
			q, err := resource.ParseQuantity(fl.Field().String())
			return err == nil && q.Sign() > 0
		},
		"qualified_name": func(fl validator.FieldLevel) bool {
			return len(validation.IsQualifiedName(fl.Field().String())) == 0
		},
		"label_value": func(fl validator.FieldLevel) bool {
			return len(validation.IsValidLabelValue(fl.Field().String())) == 0
		},
	} {
		if err := v.RegisterValidation(name, rule); err != nil {
			panic(err) // A registration failure is a programming error.
		}
	}
	return v
}

// Validate checks the declaration's schema and field values. It does not check
// whether a cluster meets the declared requirements. Field errors use YAML names
// and include all invalid fields.
func (c *ComponentContract) Validate() error {
	if c == nil {
		return fmt.Errorf("contract must not be nil")
	}
	// Preserve the sentinel error used by callers to detect unsupported versions.
	if c.APIVersion != APIVersion {
		return fmt.Errorf("%w: got %q, support %q", ErrUnsupportedAPIVersion, c.APIVersion, APIVersion)
	}
	return contractValidator.Struct(c)
}
