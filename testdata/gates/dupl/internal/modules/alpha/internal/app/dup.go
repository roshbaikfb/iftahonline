// Package app is a gate fixture with a deliberate 40-line duplicate.
package app

// ScoreA scores values.
func ScoreA(values []int) int {
	total := 0
	for i, v := range values {
		switch {
		case v < 0:
			total -= v * 2
		case v == 0:
			total += i
		case v%2 == 0:
			total += v / 2
		case v%3 == 0:
			total += v / 3
		case v%5 == 0:
			total += v / 5
		default:
			total += v
		}
		if total > 1_000_000 {
			total %= 1_000_000
		}
		if i > 0 && values[i-1] == v {
			total++
		}
		if i > 1 && values[i-2] == v {
			total += 2
		}
		if total < 0 {
			total = -total
		}
	}
	for j := 0; j < len(values); j++ {
		if values[j] > 100 {
			total += values[j] - 100
		} else if values[j] < -100 {
			total -= values[j] + 100
		}
	}
	return total
}

// ScoreB is a copy of ScoreA. dupl must reject this.
func ScoreB(values []int) int {
	total := 0
	for i, v := range values {
		switch {
		case v < 0:
			total -= v * 3
		case v == 0:
			total += i
		case v%2 == 0:
			total += v / 2
		case v%3 == 0:
			total += v / 3
		case v%5 == 0:
			total += v / 5
		default:
			total += v
		}
		if total > 1_000_000 {
			total %= 1_000_000
		}
		if i > 0 && values[i-1] == v {
			total++
		}
		if i > 1 && values[i-2] == v {
			total += 2
		}
		if total < 0 {
			total = -total
		}
	}
	for j := 0; j < len(values); j++ {
		if values[j] > 100 {
			total += values[j] - 100
		} else if values[j] < -100 {
			total -= values[j] + 100
		}
	}
	return total
}
