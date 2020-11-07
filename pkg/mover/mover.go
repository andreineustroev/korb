package mover

import (
	"context"
	"fmt"
	"time"

	"github.com/BeryJu/korb/pkg/config"
	log "github.com/sirupsen/logrus"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

type MoverJob struct {
	Name         string
	Namespace    string
	SourceVolume *corev1.PersistentVolumeClaim
	DestVolume   *corev1.PersistentVolumeClaim

	kJob    *batchv1.Job
	kClient *kubernetes.Clientset

	log *log.Entry
}

func NewMoverJob(client *kubernetes.Clientset) *MoverJob {
	return &MoverJob{
		kClient: client,
		log:     log.WithField("compoennt", "mover-job"),
	}
}

func (m *MoverJob) Start() *MoverJob {
	volumes := []corev1.Volume{
		{
			Name: "source",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: m.SourceVolume.Name,
					ReadOnly:  true,
				},
			},
		},
		{
			Name: "dest",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: m.DestVolume.Name,
					ReadOnly:  false,
				},
			},
		},
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name,
			Namespace: m.Namespace,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Volumes:       volumes,
					RestartPolicy: corev1.RestartPolicyOnFailure,
					Containers: []corev1.Container{
						{
							Name:  "mover",
							Image: config.DockerImage,
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "source",
									MountPath: "/source",
								},
								{
									Name:      "dest",
									MountPath: "/dest",
								},
							},
						},
					},
				},
			},
		},
	}
	j, err := m.kClient.BatchV1().Jobs(m.Namespace).Create(context.TODO(), job, metav1.CreateOptions{})
	if err != nil {
		// temp
		panic(err)
	}
	m.kJob = j
	return m
}

func (m *MoverJob) Wait(timeout time.Duration) error {
	err := wait.Poll(2*time.Second, timeout, func() (bool, error) {
		job, err := m.kClient.BatchV1().Jobs(m.Namespace).Get(context.TODO(), m.kJob.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if job.Status.Succeeded != int32(len(job.Spec.Template.Spec.Containers)) {
			m.log.WithField("job-name", job.Name).Debug("Waiting for job to finish...")
			return false, nil
		}
		return true, nil
	})
	if err == nil {
		// Job was run successfully, so we delete it to cleanup
		m.log.Debug("Cleaning up successful job")
		return m.Cleanup()
	}
	return err
}

func (m *MoverJob) Cleanup() error {
	err := m.kClient.BatchV1().Jobs(m.Namespace).Delete(context.TODO(), m.Name, metav1.DeleteOptions{})
	if err != nil {
		m.log.WithError(err).Debug("Failed to delete job")
		return err
	}
	selector := fmt.Sprintf("job-name=%s", m.Name)
	pods, err := m.kClient.CoreV1().Pods(m.Namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: selector})
	for _, pod := range pods.Items {
		m.kClient.CoreV1().Pods(m.Namespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
	}
	return nil
}
