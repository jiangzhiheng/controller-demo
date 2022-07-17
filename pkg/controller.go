package pkg

import (
	"context"
	v13 "k8s.io/api/core/v1"
	v1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v12 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	informer "k8s.io/client-go/informers/core/v1"
	netInformer "k8s.io/client-go/informers/networking/v1"
	"k8s.io/client-go/kubernetes"
	coreLister "k8s.io/client-go/listers/core/v1"
	netLister "k8s.io/client-go/listers/networking/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"reflect"
	"time"
)

const (
	workNum = 5
	maxRetry = 10
)


type controller struct {
	client 			kubernetes.Interface
	ingressLister	netLister.IngressLister
	serviceLister 	coreLister.ServiceLister
	queue	 		workqueue.RateLimitingInterface
}

func (c *controller) addService(obj interface{}) {
	c.enqueue(obj)
}

func (c *controller) updateService(oldObj interface{}, newObj interface{}) {
	// todo 比较annotation是否有变化
	// 判断oldObj 和newObj 是否相等
	if reflect.DeepEqual(oldObj, newObj) {
		return
	}
	// 如果不等，则将newObj添加到work queue
	c.enqueue(newObj)
}

func (c *controller) enqueue(obj interface{})  {
	// 获取job的 key， meta.GetNamespace() + "/" + meta.GetName()
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil{
		runtime.HandleError(err)
	}
	// 将key添加到 queue
	c.queue.Add(key)
}

func (c *controller) deleteIngress(obj interface{}) {
	ingress := obj.(*v1.Ingress)
	ownerReference := v12.GetControllerOf(ingress)
	// 如果service为空，则return
	if ownerReference == nil {
		return
	}
	if ownerReference.Kind != "Service" {
		return
	}
	c.queue.Add(ingress.Namespace + "/" + ingress.Name)
}

func (c *controller) Run(stopCh chan struct{}) {
	// 创建 workNum 个goroutine。从queue中消费数据并处理
	for i := 0; i < workNum; i++ {
		go wait.Until(c.worker, time.Minute, stopCh)
	}
	<- stopCh
}

func (c *controller) worker() {
	// 死循环，一直执行 processNextItem 方法
	for c.processNextItem(){

	}
}

func (c *controller) processNextItem() bool {
	item, shutdown := c.queue.Get()
	if shutdown{
		return false
	}
	// 处理完成后将item从queue中移出
	defer c.queue.Done(item)
	key := item.(string)
	err := c.syncService(key)
	if err != nil {
		c.handlerError(key, err)
	}
	return true
}

func (c *controller) syncService(key string) error {
	namespaceKey, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return err
	}

	// 当service删除的时候
	service, err := c.serviceLister.Services(namespaceKey).Get(name)
	// 如果 service已经被删除，则返回nil
	if errors.IsNotFound(err){
		return nil
	}
	// 否则，返回错误
	if err != nil {
		return err
	}

	// 新增或删除
	// 判断service 是否有"ingress/http" 的annotation
	_,ok := service.GetAnnotations()["ingress/http"]
	ingress, err := c.ingressLister.Ingresses(namespaceKey).Get(name)
	if err != nil && !errors.IsNotFound(err){
		return err
	}

	// 如果service中有注解并且ingress是不存在的，则创建ingress
	if ok && errors.IsNotFound(err){
		// create ingress
		ing := c.constructIngress(service)
		_, err := c.client.NetworkingV1().Ingresses(namespaceKey).Create(context.TODO(), ing, v12.CreateOptions{})
		if err != nil {
			return err
		}
	}else if !ok && ingress != nil{
		// 删除掉ingress
		c.client.NetworkingV1().Ingresses(namespaceKey).Delete(context.TODO(),name, v12.DeleteOptions{})
	}
	return nil
}

func (c *controller) handlerError(key string, err error) {
	//重新放入队列，等下一次继续处理,最多重试 maxRetry 次
	if c.queue.NumRequeues(key) <= maxRetry{
		c.queue.AddRateLimited(key)
		return
	}

	runtime.HandleError(err)
	// 不再记录重试次数
	c.queue.Forget(key)

}

// 创建ingress资源
func (c *controller) constructIngress(service *v13.Service) *v1.Ingress {
	ingress := v1.Ingress{}

	ingress.ObjectMeta.OwnerReferences = []v12.OwnerReference{
		*v12.NewControllerRef(service, v13.SchemeGroupVersion.WithKind("Service")),
	}
	ingress.Name = service.Name
	ingress.Namespace = service.Namespace
	pathType := v1.PathTypePrefix
	icn := "nginx"
	ingress.Spec = v1.IngressSpec{
		IngressClassName: &icn,
		Rules: []v1.IngressRule{
			{Host: "example.com",
				IngressRuleValue: v1.IngressRuleValue{
					HTTP: &v1.HTTPIngressRuleValue{
						Paths: []v1.HTTPIngressPath{
							{
								Path: "/",
								PathType: &pathType,
								Backend: v1.IngressBackend{
									Service: &v1.IngressServiceBackend{
										Name: service.Name,
										Port: v1.ServiceBackendPort{
											Number: 80,
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	return &ingress
}

func NewController(client kubernetes.Interface, serviceInformer informer.ServiceInformer, ingressInformer netInformer.IngressInformer) controller {
	c := controller{
		client:        client,
		ingressLister: ingressInformer.Lister(),
		serviceLister: serviceInformer.Lister(),
		queue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "ingressManager"),

	}

	// 1.add event handler
	serviceInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: c.addService,
		UpdateFunc: c.updateService,
	})
	ingressInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		DeleteFunc: c.deleteIngress,
	})
	return c
}