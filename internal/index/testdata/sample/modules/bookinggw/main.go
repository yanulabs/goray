package main

import "example.com/sample/modules/payments"

func main() {
	svc := payments.NewService()
	_ = svc.Charge(10)
	_ = svc.Ports()
}
