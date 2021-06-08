package v1

import (
	"net/http"

	"github.com/julienschmidt/httprouter"
)

// DEPRECATED by https://kubernetes.io/docs/reference/using-api/health-checks/
func GETHealthz(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	w.WriteHeader(http.StatusOK)
}
