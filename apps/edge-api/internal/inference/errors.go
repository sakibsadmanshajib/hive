package inference

import (
	"fmt"
	"log"
	"net/http"

	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

func writeUnsupportedParamError(w http.ResponseWriter, param, model string) {
	code := "unsupported_parameter"
	msg := fmt.Sprintf("Model does not support parameter: %s. Choose an alias with tool-calling capability.", param)
	if model != "" {
		msg = fmt.Sprintf("Model '%s' does not support parameter: %s. Choose an alias with tool-calling capability.", model, param)
	}
	apierrors.WriteErrorWithParam(w, http.StatusBadRequest, "invalid_request_error", msg, &code, param)
}

func writeModelNotFoundError(w http.ResponseWriter, model string) {
	code := "model_not_found"
	log.Printf("inference: model_not_found via routing layer model=%q", model)
	apierrors.WriteError(w, http.StatusNotFound, "invalid_request_error",
		fmt.Sprintf("The model `%s` does not exist or you do not have access to it.", model), &code)
}

func writeMissingFieldError(w http.ResponseWriter, field string) {
	apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error",
		fmt.Sprintf("Missing required parameter: '%s'.", field), nil)
}

func writeInvalidBodyError(w http.ResponseWriter) {
	apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error",
		"Invalid request body.", nil)
}
