package hakken

import (
	"encoding/json"
	"log"
	"net/url"
	"sync"
	"time"
)

type Coordinator struct {
	url.URL
}

func (c *Coordinator) UnmarshalJSON(data []byte) error {
	asMap := make(map[string]json.RawMessage)
	err := json.Unmarshal(data, &asMap)
	if err != nil {
		return err
	}

	c.Scheme = "http"
	json.Unmarshal(([]byte)(asMap["host"]), &c.Host)
	return nil
}

func (c *Coordinator) MarshalJSON() ([]byte, error) {
	objs := make(map[string]string)

	objs["host"] = c.Host
	objs["scheme"] = c.Scheme

	return json.Marshal(objs)
}

type coordinatorManager struct {
	resyncClient   coordinatorClient
	resyncTicker *time.Ticker
	pollTicker   *time.Ticker
	dropCooChan    chan *coordinatorClient

	mut sync.Mutex

	clients []coordinatorClient
	stop    chan chan error
}

func (manager *coordinatorManager) getClient() *coordinatorClient {
	manager.mut.Lock()
	defer manager.mut.Unlock()
	if manager.clients == nil || len(manager.clients) == 0 {
		return nil
	} else {
		return &manager.clients[0]
	}
}

func (manager *coordinatorManager) getClients() []coordinatorClient {
	manager.mut.Lock()
	defer manager.mut.Unlock()
	return manager.clients
}

func (manager *coordinatorManager) reportBadClient(client *coordinatorClient) {
	manager.dropCooChan <- client
}

func (manager *coordinatorManager) start() error {
	manager.mut.Lock()
	defer manager.mut.Unlock()

	if manager.stop != nil {
		return nil
	}

	log.Printf("Starting coordinatorManager at[%s]", manager.resyncClient.URL.String())
	manager.stop = make(chan chan error)
	coordinators, err := addUnknownCoordinators(nil, &manager.resyncClient)
	if err != nil {
		return err
	}

	// Already have the lock, it's not reentrant, so use lock-less method
	manager.setClientsNoLock(coordinators)

	go func() {
		for {
			select {
			case <-manager.resyncTicker.C:
				coordinators, _ := addUnknownCoordinators(manager.getClients(), &manager.resyncClient)
				manager.setClients(coordinators)
			case <-manager.pollTicker.C:
				coordinators := manager.getClients()
				for _, coo := range coordinators {
					coordinators, err = addUnknownCoordinators(coordinators, &coo)
					if err != nil {
						log.Printf("Removing coordinator[%s], because of error[%v]", coo.String(), err)
						coordinators = removeCoordinator(coordinators, &coo)
					}
				}
				manager.setClients(coordinators)
			case errChan := <-manager.stop:
				// Empty out the dropCooChan
				for {
					select {
					case <-manager.dropCooChan:
						// Do nothing
					default:
						// Be done
						errChan <- nil
						return
					}
				}
			case droppedCoo := <-manager.dropCooChan:
				clients := manager.getClients()
				manager.setClients(removeCoordinator(clients, droppedCoo))
			}
		}
	}()

	return nil
}

func (manager *coordinatorManager) Close() error {
	manager.mut.Lock()
	defer manager.mut.Unlock()

	if manager.stop == nil {
		return nil
	}

	errChan := make(chan error)
	manager.stop <- errChan

	err := <-errChan

	manager.stop = nil
	manager.clients = nil
	return err
}

func (manager *coordinatorManager) setClients(coordinators []coordinatorClient) {
	manager.mut.Lock()
	defer manager.mut.Unlock()

	log.Printf("Asked to set coordinators.  Curr[%v], new[%v]", manager.clients, coordinators)
	manager.setClientsNoLock(coordinators)
}

func (manager *coordinatorManager) setClientsNoLock(coordinators []coordinatorClient) {
	for _, newCoo := range coordinators {
		found := false
		for _, currCoo := range manager.clients {
			if newCoo == currCoo {
				found = true
			}
		}
		if !found {
			log.Printf("Adding coordinator[%s]", newCoo.String())
		}
	}

	for _, currCoo := range manager.clients {
		found := false
		for _, newCoo := range coordinators {
			if currCoo == newCoo {
				found = true
			}
		}
		if !found {
			log.Printf("Removing coordinator[%s]", currCoo.String())
		}
	}

	manager.clients = coordinators
}

func addUnknownCoordinators(coordinators []coordinatorClient, client *coordinatorClient) ([]coordinatorClient, error) {
	coos, err := client.getCoordinators()
	if err != nil {
		return coordinators, err
	}

	unknown := make([]coordinatorClient, 0, len(coos))
	for _, coo := range coos {
		found := false
		for _, known := range coordinators {
			if coo == known.Coordinator {
				found = true
			}
		}
		if !found {
			unknown = append(unknown, coordinatorClient{coo})
		}
	}

	return append(coordinators, unknown...), nil
}

func removeCoordinator(coordinators []coordinatorClient, toRemove *coordinatorClient) []coordinatorClient {
	for i, coo := range coordinators {
		if coo == *toRemove {
			retVal := make([]coordinatorClient, 0, len(coordinators) - 1)
			copy(retVal, coordinators[:i])
			return append(retVal, coordinators[i+1:]...)
		}
	}
	return coordinators
}

func getOrNil(arr []coordinatorClient, i int) *coordinatorClient {
	if len(arr) > i {
		return &arr[i]
	} else {
		return nil
	}
}
