package catalog

import (
	"fmt"
	"strings"
	"unicode"
)

// Money represents a monetary amount in the smallest currency unit (e.g. cents, pence, đồng).
type Money struct {
	amount   int64
	currency string
}

// NewMoney constructs a validated Money value object.
func NewMoney(amount int64, currency string) (Money, error) {
	if amount < 0 {
		return Money{}, ErrNegativeAmount
	}
	cur := strings.ToUpper(strings.TrimSpace(currency))
	if len(cur) != 3 {
		return Money{}, ErrInvalidCurrency
	}
	for _, r := range cur {
		if !unicode.IsLetter(r) {
			return Money{}, ErrInvalidCurrency
		}
	}
	return Money{amount: amount, currency: cur}, nil
}

// MustNewMoney constructs Money or panics if invalid.
func MustNewMoney(amount int64, currency string) Money {
	m, err := NewMoney(amount, currency)
	if err != nil {
		panic(err)
	}
	return m
}

// Amount returns the integer monetary value.
func (m Money) Amount() int64 {
	return m.amount
}

// Currency returns the 3-letter ISO currency code.
func (m Money) Currency() string {
	return m.currency
}

// IsZero returns true if amount is zero.
func (m Money) IsZero() bool {
	return m.amount == 0
}

// Add adds two Money values of identical currency.
func (m Money) Add(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, fmt.Errorf("%w: %s != %s", ErrCurrencyMismatch, m.currency, other.currency)
	}
	return Money{amount: m.amount + other.amount, currency: m.currency}, nil
}

// String returns formatted string representation.
func (m Money) String() string {
	return fmt.Sprintf("%d %s", m.amount, m.currency)
}
