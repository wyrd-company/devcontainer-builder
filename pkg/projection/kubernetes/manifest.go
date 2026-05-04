package kubernetes

import (
	"bytes"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
)

var manifestScheme = func() *runtime.Scheme {
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		panic(err)
	}
	return s
}()

// Objects returns the Kubernetes resources needed for a single-container
// projection, in creation order.
func (p *Projection) Objects() []runtime.Object {
	if p == nil || p.Pod == nil {
		return nil
	}
	return []runtime.Object{p.Pod}
}

// Objects returns the Kubernetes resources needed for a compose projection,
// in creation order.
func (p *ComposeProjection) Objects() []runtime.Object {
	if p == nil {
		return nil
	}
	var out []runtime.Object
	for _, pvc := range p.PersistentVolumeClaims {
		if pvc != nil {
			out = append(out, pvc)
		}
	}
	for _, cm := range p.ConfigMaps {
		if cm != nil {
			out = append(out, cm)
		}
	}
	for _, secret := range p.Secrets {
		if secret != nil {
			out = append(out, secret)
		}
	}
	if p.Pod != nil {
		out = append(out, p.Pod)
	}
	return out
}

// EncodeYAMLDocuments serializes Kubernetes objects as multi-document YAML.
func EncodeYAMLDocuments(objects []runtime.Object) ([]byte, error) {
	ser := json.NewYAMLSerializer(json.DefaultMetaFactory, manifestScheme, manifestScheme)
	var b bytes.Buffer
	w := json.YAMLFramer.NewFrameWriter(&b)
	for _, obj := range objects {
		if obj == nil {
			continue
		}
		prepared, err := objectWithKind(obj)
		if err != nil {
			return nil, err
		}
		if err := ser.Encode(prepared, w); err != nil {
			return nil, fmt.Errorf("encode %T: %w", obj, err)
		}
	}
	return b.Bytes(), nil
}

func objectWithKind(obj runtime.Object) (runtime.Object, error) {
	cp := obj.DeepCopyObject()
	gvks, _, err := manifestScheme.ObjectKinds(cp)
	if err != nil {
		return nil, fmt.Errorf("kind for %T: %w", obj, err)
	}
	if len(gvks) == 0 {
		return nil, fmt.Errorf("kind for %T: no registered kind", obj)
	}
	cp.GetObjectKind().SetGroupVersionKind(gvks[0])
	return cp, nil
}
