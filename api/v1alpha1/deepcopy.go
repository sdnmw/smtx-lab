package v1alpha1

import (
	"encoding/json"

	"k8s.io/apimachinery/pkg/runtime"
)

func deepCopyJSON[T any](in T) T {
	var out T
	b, err := json.Marshal(in)
	if err != nil {
		return out
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return out
	}
	return out
}

func (in *NetworkProbeLab) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := deepCopyJSON(*in)
	return &out
}

func (in *NetworkProbeLabList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := deepCopyJSON(*in)
	return &out
}

func (in *ResourceAnalyzerLab) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := deepCopyJSON(*in)
	return &out
}

func (in *ResourceAnalyzerLabList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := deepCopyJSON(*in)
	return &out
}
