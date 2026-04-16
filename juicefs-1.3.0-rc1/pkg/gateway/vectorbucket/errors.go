package vectorbucket

import (
	"errors"
	"net/http"
)

var (
	ErrValidation         = errors.New("validation failed")
	ErrConflict           = errors.New("resource already exists")
	ErrNotFound           = errors.New("resource not found")
	ErrQuotaExceeded      = errors.New("service quota exceeded")
	ErrTooManyRequests    = errors.New("request throttled")
	ErrServiceUnavailable = errors.New("service unavailable")
	ErrAccessDenied       = errors.New("access denied")
	ErrRequestTimeout     = errors.New("request timeout")
	ErrInternal           = errors.New("internal server error")
)

type APIError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Status     int    `json:"-"`
	RetryAfter int    `json:"-"`
}

func TranslateError(err error) APIError {
	switch {
	case err == nil:
		return APIError{}
	case errors.Is(err, ErrValidation):
		return APIError{Code: "ValidationException", Message: err.Error(), Status: http.StatusBadRequest}
	case errors.Is(err, ErrConflict):
		return APIError{Code: "ConflictException", Message: err.Error(), Status: http.StatusConflict}
	case errors.Is(err, ErrNotFound):
		return APIError{Code: "NotFoundException", Message: err.Error(), Status: http.StatusNotFound}
	case errors.Is(err, ErrQuotaExceeded):
		return APIError{Code: "ServiceQuotaExceededException", Message: err.Error(), Status: http.StatusPaymentRequired}
	case errors.Is(err, ErrTooManyRequests):
		return APIError{Code: "TooManyRequestsException", Message: err.Error(), Status: http.StatusTooManyRequests}
	case errors.Is(err, ErrServiceUnavailable):
		return APIError{Code: "ServiceUnavailableException", Message: err.Error(), Status: http.StatusServiceUnavailable, RetryAfter: 5}
	case errors.Is(err, ErrAccessDenied):
		return APIError{Code: "AccessDeniedException", Message: err.Error(), Status: http.StatusForbidden}
	case errors.Is(err, ErrRequestTimeout):
		return APIError{Code: "RequestTimeoutException", Message: err.Error(), Status: http.StatusRequestTimeout}
	default:
		return APIError{Code: "InternalServerException", Message: err.Error(), Status: http.StatusInternalServerError}
	}
}
