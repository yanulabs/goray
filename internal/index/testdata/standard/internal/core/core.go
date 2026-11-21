package core

type Greeter interface {
	Greet() string
}

type Service struct{}

func New() *Service { return &Service{} }

func (s *Service) Greet() string { return "hi" }
