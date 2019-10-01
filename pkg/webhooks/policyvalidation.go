package webhooks

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/golang/glog"
	kyverno "github.com/nirmata/kyverno/pkg/api/kyverno/v1alpha1"
	"github.com/nirmata/kyverno/pkg/utils"
	v1beta1 "k8s.io/api/admission/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//HandlePolicyValidation performs the validation check on policy resource
func (ws *WebhookServer) handlePolicyValidation(request *v1beta1.AdmissionRequest) *v1beta1.AdmissionResponse {
	var policy *kyverno.ClusterPolicy
	admissionResp := &v1beta1.AdmissionResponse{
		Allowed: true,
	}

	//TODO: can this happen? wont this be picked by OpenAPI spec schema ?
	raw := request.Object.Raw
	if err := json.Unmarshal(raw, &policy); err != nil {
		glog.Errorf("Failed to unmarshal policy admission request, err %v\n", err)
		return &v1beta1.AdmissionResponse{Allowed: false,
			Result: &metav1.Status{
				Message: fmt.Sprintf("Failed to unmarshal policy admission request err %v", err),
			}}
	}
	// check for uniqueness of rule names while CREATE/DELET
	admissionResp = ws.validateUniqueRuleName(policy)

	// helper function to evaluate if policy has validtion or mutation rules defined
	hasMutateOrValidate := func() bool {
		for _, rule := range policy.Spec.Rules {
			if !reflect.DeepEqual(rule.Mutation, kyverno.Mutation{}) || !reflect.DeepEqual(rule.Validation, kyverno.Validation{}) {
				return true
			}
		}
		return false
	}

	if admissionResp.Allowed {
		if hasMutateOrValidate() {
			// create mutating resource mutatingwebhookconfiguration if not present
			if err := ws.webhookRegistrationClient.CreateResourceMutatingWebhookConfiguration(); err != nil {
				glog.Error("failed to created resource mutating webhook configuration, policies wont be applied on the resource")
			}
		}
	}
	return admissionResp
}

func (ws *WebhookServer) validatePolicy(policy *kyverno.ClusterPolicy) *v1beta1.AdmissionResponse {
	admissionResp := ws.validateUniqueRuleName(policy)
	if !admissionResp.Allowed {
		return admissionResp
	}

	return ws.validateOverlayPattern(policy)
}

func (ws *WebhookServer) validateOverlayPattern(policy *kyverno.ClusterPolicy) *v1beta1.AdmissionResponse {
	for _, rule := range policy.Spec.Rules {
		if reflect.DeepEqual(rule.Validation, kyverno.Validation{}) {
			continue
		}

		if rule.Validation.Pattern == nil && len(rule.Validation.AnyPattern) == 0 {
			return &v1beta1.AdmissionResponse{
				Allowed: false,
				Result: &metav1.Status{
					Message: fmt.Sprintf("Invalid policy, neither pattern nor anyPattern found in validate rule %s", rule.Name),
				},
			}
		}

		if rule.Validation.Pattern != nil && len(rule.Validation.AnyPattern) != 0 {
			return &v1beta1.AdmissionResponse{
				Allowed: false,
				Result: &metav1.Status{
					Message: fmt.Sprintf("Invalid policy, either pattern or anyPattern is allowed in validate rule %s", rule.Name),
				},
			}
		}
	}

	return &v1beta1.AdmissionResponse{Allowed: true}
}

// Verify if the Rule names are unique within a policy
func (ws *WebhookServer) validateUniqueRuleName(policy *kyverno.ClusterPolicy) *v1beta1.AdmissionResponse {
	var ruleNames []string

	for _, rule := range policy.Spec.Rules {
		if utils.ContainsString(ruleNames, rule.Name) {
			msg := fmt.Sprintf(`The policy "%s" is invalid: duplicate rule name: "%s"`, policy.Name, rule.Name)
			glog.Errorln(msg)

			return &v1beta1.AdmissionResponse{
				Allowed: false,
				Result: &metav1.Status{
					Message: msg,
				},
			}
		}
		ruleNames = append(ruleNames, rule.Name)
	}

	glog.V(4).Infof("Policy validation passed")
	return &v1beta1.AdmissionResponse{
		Allowed: true,
	}
}