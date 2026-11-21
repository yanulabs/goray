package payments

// Gateway is implemented by Service.
type Gateway interface {
	Charge(amount int) error
}

type Service struct{}

func NewService() *Service { return &Service{} }

func (s *Service) Charge(amount int) error { return nil }

func (s *Service) Ports() int { return 0 }

func (s *Service) Start() {
	go func() {}()
}

func unusedHelper() {}
