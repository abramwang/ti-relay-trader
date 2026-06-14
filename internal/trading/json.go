package trading

import (
	"encoding/json"
	"time"

	"ti-relay-trader/internal/timeutil"
)

func optionalBusinessTime(value time.Time) *string {
	if value.IsZero() {
		return nil
	}
	formatted := timeutil.FormatRFC3339Nano(value)
	return &formatted
}

func (account Account) MarshalJSON() ([]byte, error) {
	type accountAlias Account
	return json.Marshal(struct {
		accountAlias
		UpdatedAt *string `json:"updated_at,omitempty"`
	}{
		accountAlias: accountAlias(account),
		UpdatedAt:    optionalBusinessTime(account.UpdatedAt),
	})
}

func (asset Asset) MarshalJSON() ([]byte, error) {
	type assetAlias Asset
	return json.Marshal(struct {
		assetAlias
		UpdatedAt *string `json:"updated_at,omitempty"`
	}{
		assetAlias: assetAlias(asset),
		UpdatedAt:  optionalBusinessTime(asset.UpdatedAt),
	})
}

func (position Position) MarshalJSON() ([]byte, error) {
	type positionAlias Position
	return json.Marshal(struct {
		positionAlias
		UpdatedAt *string `json:"updated_at,omitempty"`
	}{
		positionAlias: positionAlias(position),
		UpdatedAt:     optionalBusinessTime(position.UpdatedAt),
	})
}

func (order Order) MarshalJSON() ([]byte, error) {
	type orderAlias Order
	return json.Marshal(struct {
		orderAlias
		CreatedAt     *string `json:"created_at,omitempty"`
		AcceptedAt    *string `json:"accepted_at,omitempty"`
		InsertedAt    *string `json:"inserted_at,omitempty"`
		LastUpdatedAt *string `json:"last_updated_at,omitempty"`
		TerminalAt    *string `json:"terminal_at,omitempty"`
	}{
		orderAlias:    orderAlias(order),
		CreatedAt:     optionalBusinessTime(order.CreatedAt),
		AcceptedAt:    optionalBusinessTime(order.AcceptedAt),
		InsertedAt:    optionalBusinessTime(order.InsertedAt),
		LastUpdatedAt: optionalBusinessTime(order.LastUpdatedAt),
		TerminalAt:    optionalBusinessTime(order.TerminalAt),
	})
}

func (fill Fill) MarshalJSON() ([]byte, error) {
	type fillAlias Fill
	return json.Marshal(struct {
		fillAlias
		MatchedAt *string `json:"matched_at,omitempty"`
	}{
		fillAlias: fillAlias(fill),
		MatchedAt: optionalBusinessTime(fill.MatchedAt),
	})
}

func (event OrderEvent) MarshalJSON() ([]byte, error) {
	type eventAlias OrderEvent
	return json.Marshal(struct {
		eventAlias
		ProducedAt *string `json:"produced_at,omitempty"`
	}{
		eventAlias: eventAlias(event),
		ProducedAt: optionalBusinessTime(event.ProducedAt),
	})
}

func (event FillEvent) MarshalJSON() ([]byte, error) {
	type eventAlias FillEvent
	return json.Marshal(struct {
		eventAlias
		ProducedAt *string `json:"produced_at,omitempty"`
	}{
		eventAlias: eventAlias(event),
		ProducedAt: optionalBusinessTime(event.ProducedAt),
	})
}
