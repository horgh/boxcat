package main

import (
	"log"
)

func main() {
	catbox, err := harnessCatbox()
	if err != nil {
		log.Fatalf("error harnessing catbox: %s", err)
	}

	nick1 := "client1"
	client1 := newClient(nick1, "127.0.0.1", catbox.Port)
	recvChan1, sendChan1, errChan1, err := client1.start()
	if err != nil {
		log.Printf("error starting client: %s", err)
		catbox.stop()
		return
	}

	for m := range recvChan1 {
		log.Printf("got command %s", m)
	}

	close(sendChan1)

	for err := range errChan1 {
		log.Printf("got err: %s", err)
	}

	catbox.stop()
}
