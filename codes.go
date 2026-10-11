package entitler

const (
	// CodeAllowanceReached means the organisation has used its allowance of this resource.
	CodeAllowanceReached Code = "allowance_reached"
	// CodeAlreadyConnected means the payment provider account is already connected.
	CodeAlreadyConnected Code = "already_connected"
	// CodeAsOfNotAllowed means the route does not answer at another instant.
	CodeAsOfNotAllowed Code = "as_of_not_allowed"
	// CodeBilledElsewhere means another provider bills the product, so the change was refused and nothing changed.
	CodeBilledElsewhere Code = "billed_elsewhere"
	// CodeBodyTooLarge means the request body is larger than the API accepts.
	CodeBodyTooLarge Code = "body_too_large"
	// CodeBrowserNotAllowed means the key may not be used from a browser.
	CodeBrowserNotAllowed Code = "browser_not_allowed"
	// CodeCapReached means the customer holds the most live grants allowed.
	CodeCapReached Code = "cap_reached"
	// CodeCapabilityRequired means the organisation's plan lacks the capability the call needs.
	CodeCapabilityRequired Code = "capability_required"
	// CodeCarryForwardConflict means a carried-forward change conflicts with the release.
	CodeCarryForwardConflict Code = "carry_forward_conflict"
	// CodeCatalogueNotEmpty means the catalogue already holds entries.
	CodeCatalogueNotEmpty Code = "catalogue_not_empty"
	// CodeChangeLocked means the change is locked.
	CodeChangeLocked Code = "change_locked"
	// CodeChangeRequired means the call needs an open change.
	CodeChangeRequired Code = "change_required"
	// CodeConnectionFailed means no answer arrived for a batch event's request (the SDK's own code).
	CodeConnectionFailed Code = "connection_failed"
	// CodeConnectionInUse means the provider connection is in use.
	CodeConnectionInUse Code = "connection_in_use"
	// CodeConnectionMode means the provider connection's mode does not match the environment.
	CodeConnectionMode Code = "connection_mode"
	// CodeConnectionRequired means the call needs a provider connection.
	CodeConnectionRequired Code = "connection_required"
	// CodeConnectionUnreadable means the provider connection cannot be read.
	CodeConnectionUnreadable Code = "connection_unreadable"
	// CodeCredentialNotAllowed means the route refuses this kind of credential; it is never retried.
	CodeCredentialNotAllowed Code = "credential_not_allowed" //nolint:gosec // an error code, not a credential
	// CodeCustomerNotFound means no customer has that id in this environment.
	CodeCustomerNotFound Code = "customer_not_found"
	// CodeEmailTaken means the email belongs to another account.
	CodeEmailTaken Code = "email_taken"
	// CodeEmailUnconfirmed means the email is not confirmed yet.
	CodeEmailUnconfirmed Code = "email_unconfirmed"
	// CodeExperimentRunning means an experiment is running.
	CodeExperimentRunning Code = "experiment_running"
	// CodeFeatureNotFound means no feature has that key in this environment.
	CodeFeatureNotFound Code = "feature_not_found"
	// CodeHoldExpired means the hold expired before it was settled.
	CodeHoldExpired Code = "hold_expired"
	// CodeHoldReleased means the hold was released.
	CodeHoldReleased Code = "hold_released"
	// CodeHoldSettled means the hold was settled.
	CodeHoldSettled Code = "hold_settled"
	// CodeHTTPError means an answer whose body names no code, or any redirect (the SDK's own code).
	CodeHTTPError Code = "http_error"
	// CodeIdempotencyKeyRequired means the write needs an idempotency key.
	CodeIdempotencyKeyRequired Code = "idempotency_key_required"
	// CodeIdempotencyMismatch means the idempotency key was used for a different request.
	CodeIdempotencyMismatch Code = "idempotency_mismatch"
	// CodeInternal means Entitler failed to answer the request.
	CodeInternal Code = "internal"
	// CodeInvalidAmount means the amount is not a whole number in range.
	CodeInvalidAmount Code = "invalid_amount"
	// CodeInvalidBody means the request body is not valid.
	CodeInvalidBody Code = "invalid_body"
	// CodeInvalidIdempotencyKey means the idempotency key is not valid.
	CodeInvalidIdempotencyKey Code = "invalid_idempotency_key"
	// CodeInvalidOccurredAt means occurredAt is outside the instants the API accepts.
	CodeInvalidOccurredAt Code = "invalid_occurred_at"
	// CodeInvalidPath means the request path is not valid.
	CodeInvalidPath Code = "invalid_path"
	// CodeInvalidResponse means a 2xx answer the SDK cannot decode (the SDK's own code).
	CodeInvalidResponse Code = "invalid_response"
	// CodeLastEnvironment means the environment is the last one.
	CodeLastEnvironment Code = "last_environment"
	// CodeLimitReached means a limit was reached.
	CodeLimitReached Code = "limit_reached"
	// CodeListingGaps means the listings leave plans unsold.
	CodeListingGaps Code = "listing_gaps"
	// CodeListingInvalid means a listing is not valid.
	CodeListingInvalid Code = "listing_invalid"
	// CodeMethodNotAllowed means the route does not take that method.
	CodeMethodNotAllowed Code = "method_not_allowed"
	// CodeNotFound means nothing was found at that path, or the provider has never billed the customer.
	CodeNotFound Code = "not_found"
	// CodeNotListed means the plan is not listed.
	CodeNotListed Code = "not_listed"
	// CodeNotMetered means the feature is not metered.
	CodeNotMetered Code = "not_metered"
	// CodeNotSelfServe means the change is not one the customer may make alone.
	CodeNotSelfServe Code = "not_self_serve"
	// CodePaymentRequired means the payment needs the customer, or was declined.
	CodePaymentRequired Code = "payment_required"
	// CodePersonRequired means the call needs a signed-in person.
	CodePersonRequired Code = "person_required"
	// CodePlanStillBilled means a provider still bills the plan.
	CodePlanStillBilled Code = "plan_still_billed"
	// CodeProviderAccountChanged means the provider's account changed.
	CodeProviderAccountChanged Code = "provider_account_changed"
	// CodeProviderPartial means the provider applied only part of the change.
	CodeProviderPartial Code = "provider_partial"
	// CodePublicationFailed means the publication failed.
	CodePublicationFailed Code = "publication_failed"
	// CodeRateLimited means too many requests; wait as Retry-After says.
	CodeRateLimited Code = "rate_limited"
	// CodeRegistrationClosed means the sign-in provider does not let people register themselves.
	CodeRegistrationClosed Code = "registration_closed"
	// CodeReturnURLRequired means the change needs a web page, so it needs a return URL.
	CodeReturnURLRequired Code = "return_url_required"
	// CodeReviewRequired means the change needs a review.
	CodeReviewRequired Code = "review_required"
	// CodeScopeRequired means the credential lacks the scope the call needs.
	CodeScopeRequired Code = "scope_required"
	// CodeSignUpsClosed means sign-ups are closed.
	CodeSignUpsClosed Code = "sign_ups_closed"
	// CodeSnapshotKeyUnavailable means no snapshot signing key is available.
	CodeSnapshotKeyUnavailable Code = "snapshot_key_unavailable"
	// CodeStale means the provider state is stale or no provider is connected.
	CodeStale Code = "stale"
	// CodeSwitchInUse means the switch is in use.
	CodeSwitchInUse Code = "switch_in_use"
	// CodeSwitchNeeded means the call needs a switch turned on.
	CodeSwitchNeeded Code = "switch_needed"
	// CodeSwitchOff means the switch is off.
	CodeSwitchOff Code = "switch_off"
	// CodeTimedOut means a batch event's request ran past its timeout (the SDK's own code).
	CodeTimedOut Code = "timed_out"
	// CodeTooManyCustomers means the call names too many customers.
	CodeTooManyCustomers Code = "too_many_customers"
	// CodeTrackClosed means the track is closed.
	CodeTrackClosed Code = "track_closed"
	// CodeUnauthorised means the credential is missing, revoked or expired.
	CodeUnauthorised Code = "unauthorised"
	// CodeUnavailable means Entitler is unavailable for now.
	CodeUnavailable Code = "unavailable"
)
