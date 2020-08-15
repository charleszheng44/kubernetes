/*
Copyright 2014 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package rest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubereq "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/klog/v2"
	api "k8s.io/kubernetes/pkg/apis/core"
	cmstore "k8s.io/kubernetes/pkg/registry/core/configmap/storage"
)

const (
	VirtualClusterNameHeaderKey        = "virtualcluster-name"
	VirtualClusterNameConfigMapDataKey = "VirtualClusterName"
	VirtualClusterInfoConfigMapNS      = "kube-system"
	VirtualClusterInfoConfigMapName    = "virtualcluster-info"
)

// LocationStreamer is a resource that streams the contents of a particular
// location URL.
type LocationStreamer struct {
	Location        *url.URL
	Transport       http.RoundTripper
	ContentType     string
	Flush           bool
	ResponseChecker HttpResponseChecker
	RedirectChecker func(req *http.Request, via []*http.Request) error
	ConfigMap       *cmstore.REST
}

// a LocationStreamer must implement a rest.ResourceStreamer
var _ rest.ResourceStreamer = &LocationStreamer{}

func (obj *LocationStreamer) GetObjectKind() schema.ObjectKind {
	return schema.EmptyObjectKind
}
func (obj *LocationStreamer) DeepCopyObject() runtime.Object {
	panic("rest.LocationStreamer does not implement DeepCopyObject")
}

func (s *LocationStreamer) getVirtualClusterName() string {
	klog.Info("+++++++++++ get virtualcluster name")
	ctx := kubereq.WithNamespace(kubereq.NewContext(), VirtualClusterInfoConfigMapNS)
	obj, err := s.ConfigMap.Get(ctx, VirtualClusterInfoConfigMapName, &metav1.GetOptions{})
	if err != nil {
		klog.Errorf("fail to get configmap/%s: %v", VirtualClusterInfoConfigMapName, err)
		return ""
	}
	klog.Infof("+++++++++++ %v", obj)
	cm, ok := obj.(*api.ConfigMap)
	if !ok {
		klog.Error("fail to assert runtime object to api.ConfigMap")
		return ""
	}
	vcName, exist := cm.Data[VirtualClusterNameConfigMapDataKey]
	if !exist {
		klog.Errorf("can't find value associate to %s in configmap.Data", VirtualClusterNameConfigMapDataKey)
		return ""
	}
	klog.Info("found virtualcluster name, the virtualcluster name is %s", vcName)
	return vcName
}

// InputStream returns a stream with the contents of the URL location. If no location is provided,
// a null stream is returned.
func (s *LocationStreamer) InputStream(ctx context.Context, apiVersion, acceptHeader string) (stream io.ReadCloser, flush bool, contentType string, err error) {
	if s.Location == nil {
		// If no location was provided, return a null stream
		return nil, false, "", nil
	}
	transport := s.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}

	client := &http.Client{
		Transport:     transport,
		CheckRedirect: s.RedirectChecker,
	}

	req, err := http.NewRequest("GET", s.Location.String(), nil)
	if err != nil {
		return nil, false, "", fmt.Errorf("failed to construct request for %s, got %v", s.Location.String(), err)
	}
	// Pass the parent context down to the request to ensure that the resources
	// will be release properly.
	req = req.WithContext(ctx)

	vcName := s.getVirtualClusterName()
	req.Header.Add(VirtualClusterNameHeaderKey, vcName)

	resp, err := client.Do(req)
	if err != nil {
		return nil, false, "", err
	}

	if s.ResponseChecker != nil {
		if err = s.ResponseChecker.Check(resp); err != nil {
			return nil, false, "", err
		}
	}

	contentType = s.ContentType
	if len(contentType) == 0 {
		contentType = resp.Header.Get("Content-Type")
		if len(contentType) > 0 {
			contentType = strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])
		}
	}
	flush = s.Flush
	stream = resp.Body
	return
}

// PreventRedirects is a redirect checker that prevents the client from following a redirect.
func PreventRedirects(_ *http.Request, _ []*http.Request) error {
	return errors.New("redirects forbidden")
}
