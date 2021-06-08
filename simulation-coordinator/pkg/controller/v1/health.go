package v1

import "net/http"

// DEPRECATED by https://kubernetes.io/docs/reference/using-api/health-checks/
func GETHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
