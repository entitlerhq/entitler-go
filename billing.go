package entitler

import (
	"context"
)

// SubscribeParams are the options of Subscribe; nil for none.
type SubscribeParams struct {
	// Period is the billing period's key, such as monthly.
	Period string
	// Quantity is an add-on's quantity, 1 to 10,000; 0 leaves it out.
	Quantity int64
	// ReturnURL is the page the provider sends the customer back to, whether they paid or left. It
	// matters only when the next step is a web page (Stripe Checkout, 3-D Secure): a change that
	// needs one without it answers 400 return_url_required before anything changes, so pass one
	// whenever Stripe may bill the customer. A mobile app passes a universal link or app link.
	ReturnURL string
	// Register registers a customer not registered yet, if the credential may register customers.
	Register bool
	// IdempotencyKey names this change, 1 to 200 printable ASCII characters; a new UUID when "".
	// Name the event (this request), never an object: a key is never freed.
	IdempotencyKey string
}

// CancelParams are the options of Cancel and UndoPendingChange; nil acts on the plan.
type CancelParams struct {
	// AddOn names the add-on to act on, by key or public id, in place of the plan. A pointer, so
	// a blank add-on is refused rather than read as none.
	AddOn *string
	// Product names the product whose plan to act on, by key, when the customer holds several.
	Product *string
	// IdempotencyKey names this change; a new UUID when "".
	IdempotencyKey string
}

func planName(plan string) error {
	if blank(plan) {
		return argError("name the plan by its id or its key")
	}
	return nil
}

// Subscribe moves the customer to a plan, or adds an add-on (or sets its quantity, or replaces
// another, as Plans lists the move), as their own choice under self-serve rules: the move must be
// one Plans offers with ActionBuy, or the API answers 403 not_self_serve. The plan or add-on is
// named by its key or public id. It answers the next step: NextDone, NextPay (send the customer to
// URL), NextConfirming or NextManage. A declined card is an *APIError, 402 payment_required with
// Payment.Status PaymentDeclined. In an app it needs billing:self in the token's scopes (403
// scope_required) and the organisation's customer portal capability (409 capability_required).
// Other codes: 400 return_url_required, 404 customer_not_found, 409 billed_elsewhere.
func (cu *customer) Subscribe(ctx context.Context, plan string, p *SubscribeParams) (*SubscribeStep, error) {
	if err := planName(plan); err != nil {
		return nil, err
	}
	var params SubscribeParams
	if p != nil {
		params = *p
	}
	body := map[string]any{"plan": plan}
	if params.Period != "" {
		body["period"] = params.Period
	}
	if params.Quantity != 0 {
		if params.Quantity < 1 || params.Quantity > 10000 {
			return nil, argError("pass quantity as a whole number from 1 to 10000")
		}
		body["quantity"] = params.Quantity
	}
	if params.ReturnURL != "" {
		body["returnUrl"] = params.ReturnURL
	}
	if params.Register {
		body["register"] = true
	}
	key, err := optionalKey(params.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	r, err := cu.write("POST", "/subscription", key, body, true)
	if err != nil {
		return nil, err
	}
	step, m, err := fetch(ctx, cu.c, r, decodeSubscribeStep)
	if err != nil {
		return nil, err
	}
	if step.Change != nil {
		step.Change.Replayed = m.replayed
	}
	return &step, nil
}

// Cancel cancels the customer's plan, or the add-on AddOn names, as their own choice; the
// project's policy decides when (a cancel at renewal, typically). A cancel with nothing to cancel
// answers Changed false. Codes: 403 not_self_serve, 403 scope_required and 409
// capability_required in an app, 404 customer_not_found.
func (cu *customer) Cancel(ctx context.Context, p *CancelParams) (*PlanChange, error) {
	return cu.target(ctx, "", p)
}

// UndoPendingChange undoes the change booked for renewal on the plan, or on the add-on AddOn
// names. An undo with nothing pending answers Changed false. A billing change, which only the
// company books, answers 403 not_self_serve: undo it with SetPlan.
func (cu *customer) UndoPendingChange(ctx context.Context, p *CancelParams) (*PlanChange, error) {
	return cu.target(ctx, "/pending", p)
}

func (cu *customer) target(ctx context.Context, suffix string, p *CancelParams) (*PlanChange, error) {
	var params CancelParams
	if p != nil {
		params = *p
	}
	if params.AddOn != nil && params.Product != nil {
		return nil, argError("pass either addOn or product, not both")
	}
	key, err := optionalKey(params.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	var r *request
	switch {
	case params.AddOn != nil:
		seg, err := segment(*params.AddOn, "name the plan by its id or its key")
		if err != nil {
			return nil, err
		}
		r, err = cu.write("DELETE", "/subscription/add-ons/"+seg+suffix, key, nil, true)
		if err != nil {
			return nil, err
		}
	default:
		r, err = cu.write("DELETE", "/subscription"+suffix, key, nil, true)
		if err != nil {
			return nil, err
		}
		if params.Product != nil {
			if blank(*params.Product) {
				return nil, argError("name the product by its key")
			}
			q, err := queryValue(*params.Product)
			if err != nil {
				return nil, err
			}
			r.query = []string{"product=" + q}
		}
	}
	return cu.planChange(ctx, r)
}

func (cu *customer) planChange(ctx context.Context, r *request) (*PlanChange, error) {
	change, m, err := fetch(ctx, cu.c, r, decodePlanChange)
	if err != nil {
		return nil, err
	}
	change.Replayed = m.replayed
	return &change, nil
}

// BillingPortal opens the provider's page for payment details and invoices, which sends the
// customer back to returnURL. It changes no plan. A customer the provider has never billed answers
// 404 not_found, and an environment with no Stripe connection answers 409 stale; in an app it
// needs billing:self (403 scope_required).
func (cu *customer) BillingPortal(ctx context.Context, returnURL string) (*BillingPortal, error) {
	if blank(returnURL) {
		return nil, argError("pass returnURL as the page to send the customer back to")
	}
	r, err := cu.write("POST", "/billing-portal", newUUID(), map[string]any{"returnUrl": returnURL}, false)
	if err != nil {
		return nil, err
	}
	portal, _, err := fetch(ctx, cu.c, r, decodeBillingPortal)
	if err != nil {
		return nil, err
	}
	return &portal, nil
}

// SyncBilling reads the customer's state from the provider now. On the page the provider sends
// the customer back to, call it and then Plans with Revalidate, so the customer sees the plan they
// paid for without waiting for the provider's notification.
func (cu *customer) SyncBilling(ctx context.Context) (*BillingSync, error) {
	r, err := cu.write("POST", "/billing/sync", newUUID(), nil, true)
	if err != nil {
		return nil, err
	}
	sync, _, err := fetch(ctx, cu.c, r, decodeBillingSync)
	if err != nil {
		return nil, err
	}
	return &sync, nil
}
