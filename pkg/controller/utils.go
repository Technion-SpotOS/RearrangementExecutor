package controllers

import (
	"github.com/Technion-SpotOS/EvacuationPlanner/pkg/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
)

type workloadToJuggle struct {
	namespacedName types.NamespacedName
	instanceName   string
}

func evacuationPlansMatch(plan1 *v1alpha1.EvacuationPlan, plan2 *v1alpha1.EvacuationPlan) bool {
	if plan1 == nil || plan2 == nil {
		return false
	}

	if plan1.Name != plan2.Name {
		return false
	}

	if plan1.Namespace != plan2.Namespace {
		return false
	}

	return true
}
