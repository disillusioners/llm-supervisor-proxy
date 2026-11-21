package models

import (
	"log"
	"time"
)

// ResolveCredentialFor resolves ONE credential of a model into the
// executable credential shape (provider, apiKey, baseURL, wire model).
//
// It is the SINGLE implementation of that composition — the store's
// resolveWithCredential (pkg/store/database) and the JSON twin's
// ResolveInternalConfig (below) both delegate here, so a lane that
// resolves a SPECIFIC credential ref (the image-gen group's in-request
// rotation seam) composes the credential exactly like the resolver
// does, instead of re-deriving the rules.
//
// The rules (unchanged, mirrored from the pre-existing twins):
//   - the provider comes from the CREDENTIAL row,
//   - the base URL is the model's override when set, else the
//     credential's,
//   - the wire model is the model's internal model, with the peak-hour
//     override applied when the model's peak hours are active.
//
// A nil model or a nil credential yields the zero value (the callers
// treat it as "unresolvable").
func ResolveCredentialFor(model *ModelConfig, cred *CredentialConfig) ResolvedCredential {
	if model == nil || cred == nil {
		return ResolvedCredential{}
	}

	// Provider comes from the credential only.
	provider := cred.Provider

	// Resolve baseURL: model override > credential.
	baseURL := model.InternalBaseURL
	if baseURL == "" {
		baseURL = cred.BaseURL
	}

	// Determine actual model: check peak hour first.
	actualModel := model.InternalModel
	if peakModel := model.ResolvePeakHourModel(time.Now()); peakModel != "" {
		log.Printf("[PEAK-HOUR] peak hour active for model %s: using %s instead of %s",
			model.ID, peakModel, model.InternalModel)
		actualModel = peakModel
	}

	return ResolvedCredential{
		Provider:      provider,
		APIKey:        cred.APIKey,
		BaseURL:       baseURL,
		InternalModel: actualModel,
		CredentialID:  cred.ID,
	}
}
