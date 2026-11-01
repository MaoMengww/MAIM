package handler

import (
	"math"
	"strings"

	"github.com/maomeng/aim/pkg/errors"
)

func validateJudgmentModel(capability, provider, name string, inputPrice, outputPrice float64) error {
	if capability != "judge" {
		return nil
	}
	if provider != "typesafe" {
		return errors.New(errors.CodeInvalidParam, "unsupported judgment provider")
	}
	if strings.TrimSpace(name) == "" {
		return errors.New(errors.CodeInvalidParam, "judgment model name is required")
	}
	if name == "jev-latest" || name == "jev-preview" {
		return errors.New(errors.CodeInvalidParam, "judgment requires a fixed model version")
	}
	if math.IsNaN(inputPrice) || math.IsInf(inputPrice, 0) || inputPrice < 0 || math.IsNaN(outputPrice) || math.IsInf(outputPrice, 0) || outputPrice < 0 {
		return errors.New(errors.CodeInvalidParam, "invalid judgment model price")
	}
	return nil
}
