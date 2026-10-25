package kafka

// OfferAvailabilityHandlers exposes the dispatch table to the external tests.
var OfferAvailabilityHandlers = offerAvailabilityHandlers

// ErrPoison lets the external tests recognise a dead-lettered payload.
var ErrPoison = errPoison
