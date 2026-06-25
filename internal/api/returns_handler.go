package api

import (
	"net/http"

	httpErr "github.com/rdevitto86/komodo-forge-sdk-go/api/errors"
)

// ListReturnsHandler handles GET /me/orders/returns.
// Stubbed — RMA list retrieval not yet implemented.
func (s *Service) ListReturnsHandler(wtr http.ResponseWriter, req *http.Request) {
	httpErr.SendError(wtr, req, httpErr.Global.NotImplemented)
}

// CreateReturnHandler handles POST /me/orders/returns.
// Stubbed — RMA creation, order eligibility validation, and return window enforcement not yet implemented.
func (s *Service) CreateReturnHandler(wtr http.ResponseWriter, req *http.Request) {
	httpErr.SendError(wtr, req, httpErr.Global.NotImplemented)
}

// GetReturnHandler handles GET /me/orders/returns/{returnId}.
// Stubbed — RMA retrieval not yet implemented.
func (s *Service) GetReturnHandler(wtr http.ResponseWriter, req *http.Request) {
	httpErr.SendError(wtr, req, httpErr.Global.NotImplemented)
}

// CancelReturnHandler handles DELETE /me/orders/returns/{returnId}.
// Stubbed — RMA cancellation not yet implemented.
func (s *Service) CancelReturnHandler(wtr http.ResponseWriter, req *http.Request) {
	httpErr.SendError(wtr, req, httpErr.Global.NotImplemented)
}

// GetReturnInternalHandler handles GET /internal/returns/{returnId}.
// Internal route for service-to-service lookups (payments-api, etc.).
// Stubbed — not yet implemented.
func (s *Service) GetReturnInternalHandler(wtr http.ResponseWriter, req *http.Request) {
	httpErr.SendError(wtr, req, httpErr.Global.NotImplemented)
}

// ApproveReturnHandler handles PUT /internal/returns/{returnId}/approve.
// Triggers refund via payments-api on approval.
// Stubbed — not yet implemented.
func (s *Service) ApproveReturnHandler(wtr http.ResponseWriter, req *http.Request) {
	httpErr.SendError(wtr, req, httpErr.Global.NotImplemented)
}

// ReceiveReturnHandler handles PUT /internal/returns/{returnId}/receive.
// Triggers restock via shop-inventory-api and loyalty reversal via loyalty-api on receipt.
// Stubbed — not yet implemented.
func (s *Service) ReceiveReturnHandler(wtr http.ResponseWriter, req *http.Request) {
	httpErr.SendError(wtr, req, httpErr.Global.NotImplemented)
}

// RejectReturnHandler handles PUT /internal/returns/{returnId}/reject.
// Stubbed — not yet implemented.
func (s *Service) RejectReturnHandler(wtr http.ResponseWriter, req *http.Request) {
	httpErr.SendError(wtr, req, httpErr.Global.NotImplemented)
}
