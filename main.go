package main

import (
	"bufio"
	"fmt"
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

	if r.count < r.size {
		tail := (r.head + r.count) % r.size
		r.array[tail] = el
		r.count++
	} else {
		r.array[r.head] = el
		r.head = (r.head + 1) % r.size
	}
}

func (r *RingIntBuffer) Get() []int {
	r.m.Lock()
	defer r.m.Unlock()

	if r.count == 0 {
		return nil
	}

	result := make([]int, r.count)
	for i := 0; i < r.count; i++ {
		idx := (r.head + i) % r.size
		result[i] = r.array[idx]
	}

	r.head = 0
	r.count = 0
	return result
}

// Stage диный интерфейс для всех стадий пайплайна
type Stage interface {
	Run(in <-chan int, done <-chan bool) <-chan int
}

// Фильтрация положительных чисел
type PositiveFilter struct{}

func (f PositiveFilter) Run(in <-chan int, done <-chan bool) <-chan int {
	out := make(chan int)
	go func() {
		defer close(out)
		for {
			select {
			case num, ok := <-in:
				if !ok {
					return
				}
				if num > 0 {
					select {
					case out <- num:
					case <-done:
						return
					}
				}
			case <-done:
				return
			}
		}
	}()
	return out
}

// Фильтрация чисел, кратных 3 (исключая 0)
type MultipleOfThreeFilter struct{}

func (f MultipleOfThreeFilter) Run(in <-chan int, done <-chan bool) <-chan int {
	out := make(chan int)
	go func() {
		defer close(out)
		for {
			select {
			case num, ok := <-in:
				if !ok {
					return
				}
				if num != 0 && num%3 == 0 {
					select {
					case out <- num:
					case <-done:
						return
					}
				}
			case <-done:
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
	return &BufferStage{size: size, interval: interval}
}

func (b *BufferStage) Run(in <-chan int, done <-chan bool) <-chan int {
	out := make(chan int)
	buffer := NewRingIntBuffer(b.size)

	go func() {
		defer close(out)
		for {
			select {
			case num, ok := <-in:
				if !ok {
					// Выгрузка остатков при завершении источника
					data := buffer.Get()
					for _, n := range data {
						select {
						case out <- n:
						case <-done:
							return
						}
					}
					return
				}
				buffer.Push(num)
			case <-done:
				data := buffer.Get()
				for _, n := range data {
					select {
					case out <- n:
					default:
					}
				}
				return
			}
		}
	}()

	// Горутина периодической выгрузки
	go func() {
		ticker := time.NewTicker(b.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				data := buffer.Get()
				for _, num := range data {
					select {
					case out <- num:
					case <-done:
						return
					}
				}
			case <-done:
				return
			}
		}
	}()

	return out
}

type DataSourceStage struct{}

func (s DataSourceStage) Run(_ <-chan int, done <-chan bool) <-chan int {
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
				fmt.Println("Программа завершила работу!")
				return
			}

			num, err := strconv.Atoi(input)
			if err != nil {
				fmt.Println("Ошибка: введено не число. Попробуйте снова.")
				continue
			}

			select {
			case out <- num:
			case <-done:
				return
			}
		}
	}()
	return out
}

type ConsumerStage struct{}

func (c ConsumerStage) Run(in <-chan int, done <-chan bool) <-chan int {
	// Возвращаем закрытый канал
	out := make(chan int)
	close(out)

	go func() {
		for {
			select {
			case num, ok := <-in:
				if !ok {
					return
				}
				fmt.Printf("Получены данные: %d\n", num)
			case <-done:
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
	return &Pipeline{
		stages: stages,
		done:   make(chan bool),
	}
}

// Execute запускает весь пайплайн от источника до потребителя
func (p *Pipeline) Execute() {
	current := p.stages[0].Run(nil, p.done)

	for i := 1; i < len(p.stages)-1; i++ {
		current = p.stages[i].Run(current, p.done)
	}

	if len(p.stages) > 1 {
		p.stages[len(p.stages)-1].Run(current, p.done)
	}

	<-p.done
}

// Завершение работы пайплайна
func (p *Pipeline) Stop() {
	close(p.done)
}

func main() {
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
