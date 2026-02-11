package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Настройки буфера
const bufferDrainInterval = 30 * time.Second
const bufferSize = 10

type RingIntBuffer struct {
	array []int
	head  int
	count int
	size  int
	m     sync.Mutex
}

func NewRingIntBuffer(size int) *RingIntBuffer {
	log.Printf("[INFO] Creating ring buffer with size %d", size)
	return &RingIntBuffer{
		array: make([]int, size),
		head:  0,
		count: 0,
		size:  size,
	}
}

func (r *RingIntBuffer) Push(el int) {
	r.m.Lock()
	defer r.m.Unlock()

	log.Printf("[DEBUG] Buffer Push: adding element %d", el)

	if r.count < r.size {
		tail := (r.head + r.count) % r.size
		r.array[tail] = el
		r.count++
		log.Printf("[DEBUG] Buffer state: count=%d, head=%d, tail=%d", r.count, r.head, tail)
	} else {
		oldElement := r.array[r.head]
		r.array[r.head] = el
		r.head = (r.head + 1) % r.size
		log.Printf("[DEBUG] Buffer overflow: replaced %d with %d, new head=%d", oldElement, el, r.head)
	}
}

func (r *RingIntBuffer) Get() []int {
	r.m.Lock()
	defer r.m.Unlock()

	if r.count == 0 {
		log.Printf("[DEBUG] Buffer Get: buffer is empty")
		return nil
	}

	result := make([]int, r.count)
	for i := 0; i < r.count; i++ {
		idx := (r.head + i) % r.size
		result[i] = r.array[idx]
	}
	
	log.Printf("[DEBUG] Buffer Get: retrieved %d elements", len(result))

	r.head = 0
	r.count = 0
	return result
}

// Stage единый интерфейс для всех стадий пайплайна
type Stage interface {
	Run(in <-chan int, done <-chan bool) <-chan int
}

// Фильтрация положительных чисел
type PositiveFilter struct{}

func (f PositiveFilter) Run(in <-chan int, done <-chan bool) <-chan int {
	log.Printf("[INFO] Starting PositiveFilter stage")
	out := make(chan int)
	go func() {
		defer close(out)
		for {
			select {
			case num, ok := <-in:
				if !ok {
					log.Printf("[INFO] PositiveFilter: input channel closed")
					return
				}
				log.Printf("[DEBUG] PositiveFilter received: %d", num)
				if num > 0 {
					select {
					case out <- num:
						log.Printf("[DEBUG] PositiveFilter passed: %d", num)
					case <-done:
						log.Printf("[INFO] PositiveFilter: done signal received")
						return
					}
				} else {
					log.Printf("[DEBUG] PositiveFilter filtered out: %d", num)
				}
			case <-done:
				log.Printf("[INFO] PositiveFilter: done signal received")
				return
			}
		}
	}()
	return out
}

// Фильтрация чисел, кратных 3 (исключая 0)
type MultipleOfThreeFilter struct{}

func (f MultipleOfThreeFilter) Run(in <-chan int, done <-chan bool) <-chan int {
	log.Printf("[INFO] Starting MultipleOfThreeFilter stage")
	out := make(chan int)
	go func() {
		defer close(out)
		for {
			select {
			case num, ok := <-in:
				if !ok {
					log.Printf("[INFO] MultipleOfThreeFilter: input channel closed")
					return
				}
				log.Printf("[DEBUG] MultipleOfThreeFilter received: %d", num)
				if num != 0 && num%3 == 0 {
					select {
					case out <- num:
						log.Printf("[DEBUG] MultipleOfThreeFilter passed: %d", num)
					case <-done:
						log.Printf("[INFO] MultipleOfThreeFilter: done signal received")
						return
					}
				} else {
					log.Printf("[DEBUG] MultipleOfThreeFilter filtered out: %d", num)
				}
			case <-done:
				log.Printf("[INFO] MultipleOfThreeFilter: done signal received")
				return
			}
		}
	}()
	return out
}

// Буферизация с периодической выгрузкой
type BufferStage struct {
	size     int
	interval time.Duration
}

func NewBufferStage(size int, interval time.Duration) *BufferStage {
	log.Printf("[INFO] Creating BufferStage with size %d, interval %v", size, interval)
	return &BufferStage{size: size, interval: interval}
}

func (b *BufferStage) Run(in <-chan int, done <-chan bool) <-chan int {
	log.Printf("[INFO] Starting BufferStage")
	out := make(chan int)
	buffer := NewRingIntBuffer(b.size)

	go func() {
		defer close(out)
		for {
			select {
			case num, ok := <-in:
				if !ok {
					log.Printf("[INFO] BufferStage: input channel closed, draining remaining items")
					// Выгрузка остатков при завершении источника
					data := buffer.Get()
					for _, n := range data {
						select {
						case out <- n:
							log.Printf("[DEBUG] BufferStage drained: %d", n)
						case <-done:
							log.Printf("[INFO] BufferStage: done signal during drain")
							return
						}
					}
					return
				}
				buffer.Push(num)
			case <-done:
				log.Printf("[INFO] BufferStage: done signal received, draining buffer")
				data := buffer.Get()
				for _, n := range data {
					select {
					case out <- n:
						log.Printf("[DEBUG] BufferStage final drain: %d", n)
					default:
						log.Printf("[WARN] BufferStage: could not send final item %d", n)
					}
				}
				return
			}
		}
	}()

	// Горутина периодической выгрузки
	go func() {
		log.Printf("[INFO] Starting periodic buffer drain timer (every %v)", b.interval)
		ticker := time.NewTicker(b.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				log.Printf("[INFO] Periodic buffer drain triggered")
				data := buffer.Get()
				for _, num := range data {
					select {
					case out <- num:
						log.Printf("[DEBUG] Periodic drain sent: %d", num)
					case <-done:
						log.Printf("[INFO] BufferStage: done signal during periodic drain")
						return
					}
				}
			case <-done:
				log.Printf("[INFO] BufferStage: done signal received by timer routine")
				return
			}
		}
	}()

	return out
}

type DataSourceStage struct{}

func (s DataSourceStage) Run(_ <-chan int, done <-chan bool) <-chan int {
	log.Printf("[INFO] Starting DataSourceStage")
	// Игнорируем входной канал (источник)
	out := make(chan int)
	go func() {
		defer close(out)
		scanner := bufio.NewScanner(os.Stdin)
		for {
			fmt.Print("Введите число (или 'exit' для выхода): ")
			scanner.Scan()

			input := scanner.Text()
			if strings.EqualFold(input, "exit") {
				log.Printf("[INFO] DataSourceStage: exit command received")
				fmt.Println("Программа завершила работу!")
				return
			}

			num, err := strconv.Atoi(input)
			if err != nil {
				log.Printf("[ERROR] DataSourceStage: invalid input '%s': %v", input, err)
				fmt.Println("Ошибка: введено не число. Попробуйте снова.")
				continue
			}

			log.Printf("[DEBUG] DataSourceStage: sending number %d", num)
			select {
			case out <- num:
				log.Printf("[DEBUG] DataSourceStage: successfully sent %d", num)
			case <-done:
				log.Printf("[INFO] DataSourceStage: done signal received while trying to send")
				return
			}
		}
	}()
	return out
}

type ConsumerStage struct{}

func (c ConsumerStage) Run(in <-chan int, done <-chan bool) <-chan int {
	log.Printf("[INFO] Starting ConsumerStage")
	// Возвращаем закрытый канал
	out := make(chan int)
	close(out)

	go func() {
		for {
			select {
			case num, ok := <-in:
				if !ok {
					log.Printf("[INFO] ConsumerStage: input channel closed")
					return
				}
				log.Printf("[INFO] ConsumerStage: received data: %d", num)
				fmt.Printf("Получены данные: %d\n", num)
			case <-done:
				log.Printf("[INFO] ConsumerStage: done signal received")
				return
			}
		}
	}()

	return out
}

type Pipeline struct {
	stages []Stage
	done   chan bool
}

func NewPipeline(stages ...Stage) *Pipeline {
	log.Printf("[INFO] Creating pipeline with %d stages", len(stages))
	return &Pipeline{
		stages: stages,
		done:   make(chan bool),
	}
}

// Execute запускает весь пайплайн от источника до потребителя
func (p *Pipeline) Execute() {
	log.Printf("[INFO] Starting pipeline execution")
	current := p.stages[0].Run(nil, p.done)

	for i := 1; i < len(p.stages)-1; i++ {
		current = p.stages[i].Run(current, p.done)
	}

	if len(p.stages) > 1 {
		p.stages[len(p.stages)-1].Run(current, p.done)
	}

	<-p.done
	log.Printf("[INFO] Pipeline execution completed")
}

// Завершение работы пайплайна
func (p *Pipeline) Stop() {
	log.Printf("[INFO] Stopping pipeline")
	close(p.done)
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Printf("[INFO] Application started")

	// Собираем пайплайн как цепочку стадий
	pipeline := NewPipeline(
		DataSourceStage{},       // источник
		PositiveFilter{},        // фильтр > 0
		MultipleOfThreeFilter{}, // фильтр кратных 3, ≠0
		NewBufferStage(bufferSize, bufferDrainInterval), // буфер
		ConsumerStage{}, // потребитель
	)

	pipeline.Execute()
}
