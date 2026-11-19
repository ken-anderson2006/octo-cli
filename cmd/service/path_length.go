package service

import (
	"fmt"
	"unicode/utf8"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func validatePathStringLength(p *registry.ParamInfo, value string) *output.ExitError {
	length := utf8.RuneCountInString(value)
	if p.MinLength > 0 && length < p.MinLength {
		return output.ErrValidation(
			fmt.Sprintf("path parameter %s must contain at least %d character(s)", p.Name, p.MinLength),
			"pass an id matching the operation schema")
	}
	if p.MaxLength > 0 && length > p.MaxLength {
		return output.ErrValidation(
			fmt.Sprintf("path parameter %s must contain at most %d character(s)", p.Name, p.MaxLength),
			"pass an id matching the operation schema")
	}
	return nil
}
