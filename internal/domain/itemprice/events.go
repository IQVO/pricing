package itemprice

import (
	"time"

	"github.com/IQVO/pricing/internal/domain/shared"
)

// ItemPriceChanged is raised when the published view of an item price moved:
// the list price, the effective price or the end of the active sale. Its
// fields are the model's data list; Version is the guard consumers apply.
type ItemPriceChanged struct {
	shared.Header
	CatalogItemID       ID
	Currency            string
	ListPriceMinor      int64
	EffectivePriceMinor int64
	// SaleEndsAt is zero at list price.
	SaleEndsAt time.Time
}

// EventName returns "ItemPriceChanged".
func (ItemPriceChanged) EventName() string { return "ItemPriceChanged" }

// Entity returns "itemprice".
func (ItemPriceChanged) Entity() string { return entity }

// SubjectID returns the catalogItemId.
func (e ItemPriceChanged) SubjectID() string { return string(e.CatalogItemID) }

// ItemPriceWithdrawn is raised by WithdrawPrice, including the policy
// WithdrawOnDiscontinue.
type ItemPriceWithdrawn struct {
	shared.Header
	CatalogItemID ID
}

// EventName returns "ItemPriceWithdrawn".
func (ItemPriceWithdrawn) EventName() string { return "ItemPriceWithdrawn" }

// Entity returns "itemprice".
func (ItemPriceWithdrawn) Entity() string { return entity }

// SubjectID returns the catalogItemId.
func (e ItemPriceWithdrawn) SubjectID() string { return string(e.CatalogItemID) }
