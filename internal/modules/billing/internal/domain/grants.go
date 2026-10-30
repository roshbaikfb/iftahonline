package domain

// Grant is what a plan gives of one feature: a flag (0 or 1), a count
// the store may hold, or units per calendar month.
type Grant struct {
	Value     int64
	Unlimited bool
}

// Grants are a plan's grants by feature key. A feature a plan does not
// list is not granted.
type Grants map[string]Grant
