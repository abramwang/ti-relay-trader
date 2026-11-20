package futures

import (
	"regexp"
	"strings"
)

var exchangeCode = regexp.MustCompile(`^[A-Z][A-Z0-9]{0,7}$`)
var securityCode = regexp.MustCompile(`^[A-Z][A-Z0-9]{0,79}\.[A-Z][A-Z0-9]{0,7}$`)

// Encoding checks only. Membership, contract existence and trading permission
// come from directory evidence and Relay policy, not a list of venue names.
func validExchange(exchange string) bool { return exchangeCode.MatchString(exchange) }

func ValidFuturesSecurityID(id string) bool { return securityCode.MatchString(id) }

func validCatalogIdentity(b CatalogBinding) bool {
	return validExchange(b.ExchangeID) && native(b.SourceInstrumentID, 80, false) &&
		ValidFuturesSecurityID(b.SecurityID) && b.SecurityID == strings.ToUpper(b.SourceInstrumentID)+"."+b.ExchangeID
}
