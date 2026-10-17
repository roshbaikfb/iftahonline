package alpha

import betadomain "github.com/roshbaikfb/iftahonline/internal/modules/beta/internal/domain"

// Leak reaches into another module's internals. This must not compile.
func Leak() int { return betadomain.Secret }
