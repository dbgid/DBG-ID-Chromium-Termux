//go:build ignore

package main

import (
	"fmt"
	"log"

	dbgidchromium "dbgidchromium"
)

func main() {
	opts := dbgidchromium.DefaultOptions()
	opts.GUI = true

	driver, err := dbgidchromium.NewWebDriver(opts)
	if err != nil {
		log.Fatal(err)
	}
	stopInterrupt := dbgidchromium.HandleInterrupt(driver)
	defer stopInterrupt()
	defer func() {
		_, _ = driver.Quit()
	}()

	if _, err := driver.Goto("https://claimyshare.io"); err != nil {
		log.Fatal(err)
	}
	dbgidchromium.Delay(20)
	status, err := driver.DetectTurnstile()
  if err != nil {
    log.Fatal(err)
  }
  if status.HasTurnstileWidget==false || status.HasTurnstileAPI==false {
    driver.Quit()
  } 
  token, err := driver.TurnsTileToken(false) 
  if err != nil {
    log.Fatal(err)
  }
  fmt.Println("turnstile token:", token)
  driver.Quit()
}
