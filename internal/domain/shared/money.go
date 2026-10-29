package shared

import (
	"errors"
	"fmt"
)

var (
	// ErrNegativeAmount: every amount is zero or more (422 negative-amount).
	ErrNegativeAmount = errors.New("negative amount")
	// ErrCurrencyMismatch: every amount uses the one configured currency
	// (422 currency-mismatch).
	ErrCurrencyMismatch = errors.New("currency mismatch")
)

// MaxMinor is the largest amount in minor units (ADR 0002, A6). A plain
// literal on purpose: with at most 100 lines of at most 1000 units each, a
// quote total times 10000 basis points still fits an int64.
const MaxMinor = 1000000000

// Money is an amount in integer minor units of one ISO 4217 currency.
type Money struct {
	AmountMinor int64
	Currency    string
}

// IsZero reports whether m is the zero value (no currency, no amount): the
// "absent" form of an optional amount.
func (m Money) IsZero() bool { return m == Money{} }

// Check reports why m is not a valid amount of the configured currency:
// ErrNegativeAmount, ErrCurrencyMismatch, or ErrValidation above MaxMinor.
func (m Money) Check(currency string) error {
	if m.AmountMinor < 0 {
		return fmt.Errorf("%w: %d", ErrNegativeAmount, m.AmountMinor)
	}
	if m.Currency != currency {
		return fmt.Errorf("%w: %q is not the configured currency %q", ErrCurrencyMismatch, m.Currency, currency)
	}
	if m.AmountMinor > MaxMinor {
		return Invalid("amount %d is above the maximum of %d minor units", m.AmountMinor, MaxMinor)
	}
	return nil
}

// ValidCurrency reports whether c is three upper-case ASCII letters.
func ValidCurrency(c string) bool {
	if len(c) != 3 {
		return false
	}
	for i := 0; i < 3; i++ {
		if c[i] < 'A' || c[i] > 'Z' {
			return false
		}
	}
	return true
}

// Tier is a delivery-promise service tier.
type Tier string

// The four service tiers.
const (
	TierSameDay  Tier = "SAME_DAY"
	TierNextDay  Tier = "NEXT_DAY"
	TierTwoDay   Tier = "TWO_DAY"
	TierStandard Tier = "STANDARD"
)

// Tiers lists the four tiers in speed order.
func Tiers() []Tier { return []Tier{TierSameDay, TierNextDay, TierTwoDay, TierStandard} }

// Valid reports whether t is one of the four tiers.
func (t Tier) Valid() bool {
	return t == TierSameDay || t == TierNextDay || t == TierTwoDay || t == TierStandard
}
