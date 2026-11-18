package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/openjiuwen/agent-festival/go/internal/adopt"
)

// Typed control-protocol errors (AR-07, #27). Every /exec and /agent endpoint
// answers an error as {"error": message, "code": CODE}. The code, not the
// HTTP status, tells the executor what happened: losing the lease, an input
// conflict, a storage failure and a rejected judgment all need different
// handling, and several of them share 409.
var errJudgmentRejected = errors.New("POLICY_REJECTED")

// errorCode maps an error to its HTTP status and protocol code.
func errorCode(err error) (int, string) {
	switch {
	case errors.Is(err, errStorage):
		return http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE" // nothing was recorded; retry after repair
	case errors.Is(err, errFenced):
		return http.StatusConflict, "FENCED" // stop writing: the lease belongs to another executor or the run stopped
	case errors.Is(err, errCallConflict):
		return http.StatusConflict, "INPUT_CONFLICT" // same identity, different input or binding: never resend
	case errors.Is(err, errModelBinding):
		return http.StatusConflict, "MODEL_BINDING_MISMATCH" // run blocked before the model call
	case errors.Is(err, errJudgmentRejected):
		return http.StatusUnprocessableEntity, "POLICY_REJECTED" // content rejected: a revised judgment may be submitted
	default:
		return http.StatusBadRequest, "INVALID_REQUEST"
	}
}

// adoptionError classifies an adoption the gate did not accept. The judgment's
// content was checked before adoption, so none of these is a policy rejection:
// a revised judgment would not help, and calling the model again for a storage
// failure is exactly what AR-07 must prevent.
func adoptionError(out adopt.Outcome) error {
	switch {
	case out.Event != nil && out.Event.Outcome.Status == "conflict":
		// same operation, different decision digest: the first adoption stands
		return fmt.Errorf("%w: adoption: %s", errCallConflict, out.Detail)
	case out.Event == nil && strings.HasPrefix(out.Detail, "ledger append failed"):
		return fmt.Errorf("%w: adoption ledger: %s", errStorage, out.Detail)
	default:
		// a request the framework built wrongly, or an exhausted ledger
		return fmt.Errorf("adoption %s: %s", out.Status, out.Detail)
	}
}
