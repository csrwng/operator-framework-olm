// +build !ignore_autogenerated

// This file was autogenerated by deepcopy-gen. Do not edit it manually!

package v1alpha1

import (
	conversion "k8s.io/apimachinery/pkg/conversion"
	runtime "k8s.io/apimachinery/pkg/runtime"
	reflect "reflect"
)

// GetGeneratedDeepCopyFuncs returns the generated funcs, since we aren't registering them.
//
// Deprecated: deepcopy registration will go away when static deepcopy is fully implemented.
func GetGeneratedDeepCopyFuncs() []conversion.GeneratedDeepCopyFunc {
	return []conversion.GeneratedDeepCopyFunc{
		{Fn: func(in interface{}, out interface{}, c *conversion.Cloner) error {
			in.(*CatalogSource).DeepCopyInto(out.(*CatalogSource))
			return nil
		}, InType: reflect.TypeOf(&CatalogSource{})},
		{Fn: func(in interface{}, out interface{}, c *conversion.Cloner) error {
			in.(*CatalogSourceList).DeepCopyInto(out.(*CatalogSourceList))
			return nil
		}, InType: reflect.TypeOf(&CatalogSourceList{})},
		{Fn: func(in interface{}, out interface{}, c *conversion.Cloner) error {
			in.(*CatalogSourceSpec).DeepCopyInto(out.(*CatalogSourceSpec))
			return nil
		}, InType: reflect.TypeOf(&CatalogSourceSpec{})},
		{Fn: func(in interface{}, out interface{}, c *conversion.Cloner) error {
			in.(*Icon).DeepCopyInto(out.(*Icon))
			return nil
		}, InType: reflect.TypeOf(&Icon{})},
	}
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CatalogSource) DeepCopyInto(out *CatalogSource) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CatalogSource.
func (in *CatalogSource) DeepCopy() *CatalogSource {
	if in == nil {
		return nil
	}
	out := new(CatalogSource)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *CatalogSource) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	} else {
		return nil
	}
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CatalogSourceList) DeepCopyInto(out *CatalogSourceList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	out.ListMeta = in.ListMeta
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]CatalogSource, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CatalogSourceList.
func (in *CatalogSourceList) DeepCopy() *CatalogSourceList {
	if in == nil {
		return nil
	}
	out := new(CatalogSourceList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *CatalogSourceList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	} else {
		return nil
	}
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CatalogSourceSpec) DeepCopyInto(out *CatalogSourceSpec) {
	*out = *in
	if in.Secrets != nil {
		in, out := &in.Secrets, &out.Secrets
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	out.Icon = in.Icon
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CatalogSourceSpec.
func (in *CatalogSourceSpec) DeepCopy() *CatalogSourceSpec {
	if in == nil {
		return nil
	}
	out := new(CatalogSourceSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Icon) DeepCopyInto(out *Icon) {
	*out = *in
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Icon.
func (in *Icon) DeepCopy() *Icon {
	if in == nil {
		return nil
	}
	out := new(Icon)
	in.DeepCopyInto(out)
	return out
}
