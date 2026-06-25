package api

// Service is the top-level handler receiver for order-api.
type Service struct {
	*OrderService
}

// NewService constructs a Service from the provided OrderService.
func NewService(os *OrderService) *Service {
	return &Service{OrderService: os}
}
