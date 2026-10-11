package entitler

import (
	"context"
	"iter"
	"strings"
)

// Server is the server client, built from a secret key: it runs on your servers and acts on any
// customer. It is safe for concurrent use; share one, as you would an http.Client.
type Server struct {
	c caller
	// Customers lists and creates customers.
	Customers *CustomersService
}

// NewServer creates the server client from a secret project key from the dashboard. It refuses a
// publishable key (ent_pk_…): a key that holds only product scopes belongs in an app, and a
// secret key never does. Construction errors wrap [ErrInvalidArgument].
func NewServer(key string, opts ...ServerOption) (*Server, error) {
	key = trimCredential(key)
	if key == "" {
		return nil, argError("provide an Entitler API key from the dashboard")
	}
	if strings.HasPrefix(key, "ent_pk_") {
		return nil, argError("a publishable key belongs in entitler.Client. Use a secret key from the dashboard on your server")
	}
	s := defaults()
	for _, o := range opts {
		if o != nil {
			o.applyServer(&s)
		}
	}
	if s.err != nil {
		return nil, s.err
	}
	e := newEngine(s, &credentials{kind: principalKey, key: key}, false, "server")
	return newServerFrom(caller{e: e, timeout: s.timeout}), nil
}

func newServerFrom(c caller) *Server {
	return &Server{c: c, Customers: &CustomersService{c: c}}
}

// With answers a client derived from this one for some calls, sharing its transport, cache,
// generations and closed state: srv.With(entitler.WithTimeout(2*time.Second)) sets those calls'
// per-attempt timeout. Only WithTimeout applies; any other option, or an invalid timeout, makes
// every call of the derived client fail with an error wrapping ErrInvalidArgument.
func (s *Server) With(opts ...Option) *Server {
	return newServerFrom(s.c.with(opts))
}

// Close cancels every call in flight, drops the in-memory cache (a custom store is left as it is)
// and makes every later call, of this client and every client derived with With, fail with
// [ErrClosed]. An HTTP client given with WithHTTPClient stays open. Closing twice is safe.
func (s *Server) Close() error {
	s.c.e.shutdown()
	return nil
}

// String describes the client by kind and base URL, never its key.
func (s *Server) String() string {
	return "entitler.Server(" + s.c.e.baseURL + ")"
}

// Customer answers the customer with your app's id, without a request. A blank id, or one made
// only of dots or not valid UTF-8, fails each of its methods instead. Identity-token customers
// have ids of the form <provider id>:<subject>.
func (s *Server) Customer(id string) *ServerCustomer {
	return &ServerCustomer{customer: newServerCustomer(s.c, id)}
}

// Pricing answers the pricing on sale, signed out, through the answer cache. Pass Visitor, a
// visitor id the app keeps for the person, to keep their experiment arm. Pricing is always
// computed now.
func (s *Server) Pricing(ctx context.Context, p *PricingParams) (*Pricing, error) {
	r := &request{method: "GET", path: "/pricing", cached: true}
	if err := pricingParams(r, p); err != nil {
		return nil, err
	}
	pricing, m, err := fetch(ctx, s.c, r, decodePricing)
	if err != nil {
		return nil, err
	}
	pricing.Stale = m.stale
	return &pricing, nil
}

// Features answers the catalogue's features, through the answer cache.
func (s *Server) Features(ctx context.Context) (*FeatureList, error) {
	list, m, err := fetch(ctx, s.c, &request{method: "GET", path: "/pricing/features", cached: true}, decodeFeatureList)
	if err != nil {
		return nil, err
	}
	list.Stale = m.stale
	return &list, nil
}

// Scopes answers the scopes the key holds, for deciding which controls to show; the server decides
// on every call regardless. Each call asks again.
func (s *Server) Scopes(ctx context.Context) (*Scopes, error) {
	return scopes(ctx, s.c)
}

func scopes(ctx context.Context, c caller) (*Scopes, error) {
	answer, _, err := fetch(ctx, c, &request{method: "GET", path: "/keys/self"}, decodeScopes)
	if err != nil {
		return nil, err
	}
	return &answer, nil
}

// SnapshotKeys answers the public keys that verify snapshots. It sends no credential, so it works
// while a token cannot be had.
func (s *Server) SnapshotKeys(ctx context.Context) (*SnapshotKeys, error) {
	return snapshotKeys(ctx, s.c)
}

// CustomersService lists and creates customers.
type CustomersService struct {
	c caller
}

// ListCustomersParams are the options of the customer list; nil for none.
type ListCustomersParams struct {
	// Q searches names, emails and ids.
	Q string
	// Cohort filters by cohort.
	Cohort string
	// Track filters by track id.
	Track string
	// IncludeTest includes test customers.
	IncludeTest bool
	// Cursor starts at the page a previous page's Next names.
	Cursor string
}

// Page answers one page of the customer list, from Cursor when given.
func (s *CustomersService) Page(ctx context.Context, p *ListCustomersParams) (*CustomerPage, error) {
	var params ListCustomersParams
	if p != nil {
		params = *p
	}
	r := &request{method: "GET", path: "/customers"}
	for _, q := range []struct{ name, value string }{
		{"q", params.Q}, {"cohort", params.Cohort}, {"track", params.Track}, {"cursor", params.Cursor},
	} {
		if q.value == "" {
			continue
		}
		v, err := queryValue(q.value)
		if err != nil {
			return nil, err
		}
		r.query = append(r.query, q.name+"="+v)
	}
	if params.IncludeTest {
		r.query = append(r.query, "includeTest=true")
	}
	page, _, err := fetch(ctx, s.c, r, decodeCustomerPage)
	if err != nil {
		return nil, err
	}
	return &page, nil
}

// List iterates every customer from Cursor on, fetching each page only when the loop reaches it.
// A failed page ends the loop with its error.
func (s *CustomersService) List(ctx context.Context, p *ListCustomersParams) iter.Seq2[CustomerSummary, error] {
	var params ListCustomersParams
	if p != nil {
		params = *p
	}
	return func(yield func(CustomerSummary, error) bool) {
		for {
			page, err := s.Page(ctx, &params)
			if err != nil {
				yield(CustomerSummary{}, err)
				return
			}
			for _, item := range page.Items {
				if !yield(item, nil) {
					return
				}
			}
			if page.Next == "" {
				return
			}
			params.Cursor = page.Next
		}
	}
}

// CreateCustomerParams are the details of a new customer.
type CreateCustomerParams struct {
	// ExternalID is your app's id for the customer.
	ExternalID string
	// Name is the customer's name.
	Name string
	// Email is the customer's email, or "".
	Email string
	// Plan is the plan to start them on, by key or public id, or "" for the default.
	Plan string
	// Period is the plan's period key, or "".
	Period string
	// Metadata is your app's metadata.
	Metadata map[string]string
	// IdempotencyKey names this creation; a new UUID when "".
	IdempotencyKey string
}

// Create creates a customer, for import tools; apps register customers with ServerCustomer.Register.
func (s *CustomersService) Create(ctx context.Context, p CreateCustomerParams) (*CustomerDetail, error) {
	if blank(p.ExternalID) {
		return nil, argError("provide the id your app uses for the customer")
	}
	key, err := optionalKey(p.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"externalId": p.ExternalID, "name": p.Name}
	for name, value := range map[string]string{"email": p.Email, "plan": p.Plan, "period": p.Period} {
		if value != "" {
			body[name] = value
		}
	}
	if p.Metadata != nil {
		body["metadata"] = p.Metadata
	}
	r := &request{method: "POST", path: "/customers", key: key, body: marshalBody(body)}
	detail, _, err := fetch(ctx, s.c, r, decodeCustomerDetail)
	if err != nil {
		return nil, err
	}
	return &detail, nil
}
