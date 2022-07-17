package main

import (
	"github.com/harryzjiang/controller-demo/pkg"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"log"
)

func main() {
	// 1. config
	config, err := clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
	if err != nil{
		inClusterConfig, err := rest.InClusterConfig()
		if err != nil {
			log.Fatalln("can not get config")
		}
		config = inClusterConfig
	}

	// 2. client
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalln("cat not create client")
	}

	// 3. get informer
	factory := informers.NewSharedInformerFactory(clientset, 0)
	serviceInformer := factory.Core().V1().Services()
	ingressInformer := factory.Networking().V1().Ingresses()

	controller := pkg.NewController(clientset, serviceInformer, ingressInformer)

	stopCh := make(chan struct{})

	// 4. start informer
	factory.Start(stopCh)
	factory.WaitForCacheSync(stopCh)

	// 5. start controller
	controller.Run(stopCh)

}
