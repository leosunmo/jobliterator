package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"time"

	"github.com/ericchiang/k8s"
	"github.com/ghodss/yaml"
)

type kubePod struct {
	name      string
	namespace string
	phase     string
}

type kubeJobSet map[string][]kubePod

type kubeJob struct {
	name      string
	namespace string
	age       int
	pods      []kubePod
}

func (js kubeJobSet) Add(job string, pod kubePod) {
	_, ok := js[job]
	if !ok {
		js[job] = make([]kubePod, 0, 20)
	}
	js[job] = append(js[job], pod)
}

func loadClient(kubeconfigPath, kubeContext string, inCluster bool) (*k8s.Client, error) {
	if inCluster {
		client, err := k8s.NewInClusterClient()
		if err != nil {
			return nil, fmt.Errorf("Failed to create in-cluster client: %v", err)
		}
		return client, nil
	}
	data, err := ioutil.ReadFile(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("Failed to read kubeconfig: %v", err)
	}

	// Unmarshal YAML into a Kubernetes config object.
	var config k8s.Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("Failed to unmarshal kubeconfig: %v", err)
	}
	if kubeContext != "" {
		config.CurrentContext = kubeContext
	}
	return k8s.NewClient(&config)
}

func getOrphanedPods(client *k8s.Client, kubeNamespace string) ([]kubeJob, error) {
	var opJobs []kubeJob
	opJobSet := make(kubeJobSet)
	pods, podErr := client.CoreV1().ListPods(context.Background(), kubeNamespace)
	if podErr != nil {
		return nil, fmt.Errorf("ERROR: %s.", podErr.Error())
	} else {
		if len(pods.Items) > 0 {
			for _, p := range pods.Items {
				pl := p.Metadata.GetLabels()
				if val, ok := pl["job-name"]; ok {
					fmt.Printf("[DEBUG]: Pod %s\n\t\tJob-name value: %s\t", p.Metadata.GetName, val)
					jobCheck, err := client.BatchV1().GetJob(context.Background(), val, kubeNamespace)
					if err != nil {
						if apiErr, ok := err.(*k8s.APIError); ok {
							if apiErr.Code == 404 {
								jobCheck = nil
							}
						} else {
							return nil, fmt.Errorf("Error getting job: %s", err.Error())
						}
					}
					if jobCheck == nil {
						kp := kubePod{name: p.Metadata.GetName(), namespace: p.Metadata.GetNamespace(), phase: p.Status.GetPhase()}
						opJobSet.Add(val, kp)
					}
				}
			}
		} else {
			return nil, fmt.Errorf("Unable to find any pods.")
		}
	}
	for k, v := range opJobSet {
		opJobs = append(opJobs, kubeJob{name: k, namespace: kubeNamespace, age: 0, pods: v})
	}
	return opJobs, nil
}

func main() {
	kubeconfigPath := flag.String("kubeconfig", "./config", "path to the kubeconfig file")
	inCluster := flag.Bool("in-cluster", false, "Use in-cluster credentials")
	kubeContext := flag.String("context", "", "override current-context (default 'current-context' in kubeconfig)")
	kubeNamespace := flag.String("namespace", "", "specific namespace (default all namespaces)")
	deleteJobs := flag.Bool("f", false, "Delete the jobs/pods (default simulate without deleting)")
	orphanedPods := flag.Bool("o", false, "Search for orphaned job pods. Deletes them if \"-f\" is set.")
	olderThanDays := flag.Int("days", 7, "set delete threshold in days")
	flag.Parse()
	//uses the current context in kubeconfig unless overriden using '-context'
	client, err := loadClient(*kubeconfigPath, *kubeContext, *inCluster)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	// Retrive a list of all jobs in the current context and namespace
	jobs, err := client.BatchV1().ListJobs(context.Background(), *kubeNamespace)
	if err != nil {
		panic(err.Error())
	}

	now := time.Now()
	var eligibleJobs []kubeJob
	for _, j := range jobs.Items {
		if *j.Status.Active == 1 {
			continue
		}
		completionTime := time.Unix(j.Status.GetCompletionTime().GetSeconds(), 0)
		daysOld := int(now.Sub(completionTime).Hours() / 24)
		if daysOld >= *olderThanDays {
			eligibleJobs = append(eligibleJobs, kubeJob{name: *j.Metadata.Name, namespace: *j.Metadata.Namespace, age: daysOld})
		}
	}

	if *deleteJobs {
		for _, dj := range eligibleJobs {
			fmt.Printf("Deleting job: %s\tNamespace:%s\tAge:%vd\n", dj.name, dj.namespace, dj.age)
			// First use the job label to find the corresponding pods to delete
			podLS := new(k8s.LabelSelector)
			podLS.Eq("job-name", dj.name)
			pods, podErr := client.CoreV1().ListPods(context.Background(), *kubeNamespace, podLS.Selector())
			if podErr != nil {
				fmt.Printf("Unable to list jobs with label job-name=%s. Skipping this job.", dj.name)
				fmt.Printf("ERROR: Job %s skipped. %s.", dj.name, podErr.Error())
				continue
			}
			var eligiblePods []kubePod
			if len(pods.Items) > 0 {
				for _, p := range pods.Items {
					// Build a slice of eligible jobs to avoid calling the API more than needed
					if *p.Status.Phase == "Succeeded" || *p.Status.Phase == "Failed" {
						eligiblePods = append(eligiblePods, kubePod{name: p.Metadata.GetName(), namespace: p.Metadata.GetNamespace(), phase: p.Status.GetPhase()})
					} else {
						fmt.Printf("\tPod associated with %s is not in \"Succeeded\" or \"Failed\" phase but job is complete.", dj.name)
						fmt.Printf("\tPod %s is in phase %s, skipping.\n", p.Metadata.GetName(), p.Status.GetPhase())
					}
				}
				if len(eligiblePods) > 0 {
					for _, dp := range eligiblePods {
						fmt.Printf("\tDeleting pod: %s\tPhase: %s\n", dp.name, dp.phase)
						podErr = client.CoreV1().DeletePod(context.Background(), dp.name, dp.namespace)
						if podErr != nil {
							fmt.Printf("\tUnable to delete pod %s. Error: %s\n", dp.name, podErr.Error())
							continue
						}
					}
				} else {
					fmt.Printf("\tNo pods eligible for deletion associated with job %s.\n", dj.name)
				}
			} else {
				fmt.Printf("\tNo pods associated with job %s.\n", dj.name)
			}

			err2 := client.BatchV1().DeleteJob(context.Background(), dj.name, dj.namespace)
			if err2 != nil {
				fmt.Println("Unable to delete job %s.\n Error: %v\n", dj.name, err2.Error())
				continue
			}
		}
		if *orphanedPods {
			opCount := 0
			fmt.Println("==============================")
			fmt.Println("Searching for orphaned pods...")
			fmt.Println("==============================")
			opJobs, err := getOrphanedPods(client, *kubeNamespace)
			if err != nil {
				fmt.Printf("Error fetching orphaned pods: %s", err.Error())
			} else {
				for _, j := range opJobs {
					fmt.Printf("Job: %s\tNamespace:%s\n", j.name, j.namespace)
					if len(j.pods) < 1 {
						fmt.Printf("Unable to find any pods associated with job %s.\n", j.name)
						continue
					}
					for _, op := range j.pods {
						if op.phase == "Succeeded" || op.phase == "Failed" {
							opCount++
							fmt.Printf("\tDeleting pod: %s\tNamespace: %s\tPhase: %s\n", op.name, op.namespace, op.phase)
							podErr := client.CoreV1().DeletePod(context.Background(), op.name, op.namespace)
							if podErr != nil {
								fmt.Printf("\tUnable to delete pod %s. Error: %s\n", op.name, podErr.Error())
								continue
							}
						} else {
							fmt.Printf("\tPod %s is not in \"Succeeded\" or \"Failed\" phase but appears oprhaned.\n", op.name)
							fmt.Printf("\tPod %s is in phase %s, skipping.\n", op.name, op.phase)
						}

					}

				}
			}
			fmt.Printf("Total orphaned Pods: %v beloning to %v jobs.\n", opCount, len(opJobs))
		}
	} else {
		fmt.Println("Jobs eligible for deletion with -f flag:")
		for _, dj := range eligibleJobs {
			fmt.Printf("Name: %s\tNamespace: %s\tAge:%vd\n", dj.name, dj.namespace, dj.age)
			podLS := new(k8s.LabelSelector)
			podLS.Eq("job-name", dj.name)
			pods, podErr := client.CoreV1().ListPods(context.Background(), *kubeNamespace, podLS.Selector())
			if podErr != nil {
				fmt.Printf("Unable to list jobs with label job-name=%s. Skipping this job.", dj.name)
				fmt.Printf("ERROR: Job %s skipped. %s.", dj.name, podErr.Error())
				continue
			}
			var eligiblePods []kubePod
			if len(pods.Items) > 0 {
				for _, p := range pods.Items {
					if *p.Status.Phase == "Succeeded" || *p.Status.Phase == "Failed" {
						eligiblePods = append(eligiblePods, kubePod{name: p.Metadata.GetName(), namespace: p.Metadata.GetNamespace(), phase: p.Status.GetPhase()})
					} else {
						fmt.Printf("\tPod associated with %s is not in \"Succeeded\" or \"Failed\" phase but job is complete.", dj.name)
						fmt.Printf("\tPod %s is in phase %s, skipping.", p.Metadata.GetName(), p.Status.GetPhase())
					}
				}
				if len(eligiblePods) > 0 {
					for _, dp := range eligiblePods {
						fmt.Printf("\tPod: %s\tNamespace: %s\tPhase: %s\n", dp.name, dp.namespace, dp.phase)
					}
				} else {
					fmt.Printf("\tNo pods eligible for deletion associated with job %s.\n", dj.name)
				}
			} else {
				fmt.Printf("\tNo pods associated with job %s.\n", dj.name)
			}
		}
		fmt.Printf("Total Jobs: %v\n", len(eligibleJobs))
		if *orphanedPods {
			opCount := 0
			fmt.Println("==============================")
			fmt.Println("Searching for orphaned pods...")
			fmt.Println("==============================")
			opJobs, err := getOrphanedPods(client, *kubeNamespace)
			if err != nil {
				fmt.Printf("Error fetching orphaned pods: %s", err.Error())
			} else {
				for _, j := range opJobs {
					fmt.Printf("Job: %s\tNamespace: %s\n", j.name, j.namespace)
					for _, op := range j.pods {
						if op.phase == "Succeeded" || op.phase == "Failed" {
							opCount++
							fmt.Printf("\tPod: %s\tNamespace: %s\tPhase: %s\n", op.name, op.namespace, op.phase)
						} else {
							fmt.Printf("\tPod %s is not in \"Succeeded\" or \"Failed\" phase but appears oprhaned.\n", op.name)
							fmt.Printf("\tPod %s is in phase %s, skipping.\n", op.name, op.phase)
						}
					}
				}
			}
			fmt.Printf("Total orphaned Pods: %v beloning to %v jobs.\n", opCount, len(opJobs))
		}
	}
}
