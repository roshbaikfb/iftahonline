package domain

import "net/http"

// StatusOK leaks transport into the domain. depguard must reject this.
func StatusOK() int { return http.StatusOK }
