package checker

import (
	"context"
	"fmt"
	"net/http"
	urlpkg "net/url"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Checker struct {
	mu     sync.Mutex
	checks map[string]*Check
}

var GlobalChecker *Checker

func (c *Checker) generateId() (string, error) {
	uuidObj, err := uuid.NewRandom()
	if err != nil {
		return "", err
	}

	return uuidObj.String(), nil
}

func (c *Checker) validateCheck(check *Check) error {
	_, err := urlpkg.Parse(check.Url)
	if err != nil {
		return err
	}

	if check.Interval <= 0 {
		return fmt.Errorf("invalid interval value")
	}

	return nil
}

func (c *Checker) equal(a *Check, b *Check) bool {
	return a.Url == b.Url && a.Interval == b.Interval
}

func (c *Checker) CreateOrUpdateCheck(id string, newCheck *Check) (*Check, error) {
	var err error
	var checkId string

	if id == "" {
		checkId, err = c.generateId()
		if err != nil {
			return nil, err
		}
	} else {
		checkId = id
	}

	err = c.validateCheck(newCheck)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	needCancel := false

	oldCheck, ok := c.checks[checkId]
	if ok {
		if c.equal(oldCheck, newCheck) {
			// no change
			return oldCheck, nil
		} else {
			// changed, need restart
			needCancel = true
		}
	}

	newCheck.id = checkId
	newCheck.reason = "<none>"
	ctx, cancel := context.WithCancel(context.Background())
	newCheck.cancel = cancel

	c.checks[id] = newCheck

	go newCheck.Check(ctx)

	if needCancel {
		oldCheck.cancel()
	}

	return newCheck, nil
}

func (c *Checker) DeleteCheck(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if check, ok := c.checks[id]; !ok {
		return fmt.Errorf("couldn't find check %s", id)
	} else {
		delete(c.checks, id)
		check.cancel()
		return nil
	}
}

func (c *Checker) GetCheck(id string) *Check {
	if chk, ok := c.checks[id]; ok {
		return chk
	} else {
		return nil
	}
}

type Check struct {
	// Spec
	Url      string
	Interval time.Duration

	// Status
	id     string
	reason string

	// Others
	cancel context.CancelFunc
}

func (c *Check) Id() string {
	return c.id
}

func (c *Check) Reason() string {
	return c.reason
}

func (c *Check) Check(ctx context.Context) {
	ticker := time.NewTicker(c.Interval)
	if ticker == nil {
		c.reason = "failed to create ticker"
		return
	}

	for {
		select {
		case <-ticker.C:
			err := c.checkHTTP(ctx, c.Url)
			if err != nil {
				c.reason = err.Error()
			}
		case <-ctx.Done():
			return
		}
	}
}

func (c *Check) checkHTTP(ctx context.Context, url string) error {
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return fmt.Errorf("http redirect is not supported")
		},
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("got non-200 error code: %d", resp.StatusCode)
	}

	return nil
}

func init() {
	GlobalChecker = &Checker{
		checks: make(map[string]*Check),
	}
}
