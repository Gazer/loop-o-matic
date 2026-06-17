package app

type UsageError struct {
	Message string
}

func (e UsageError) Error() string { return e.Message }

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if _, ok := err.(UsageError); ok {
		return 2
	}
	return 1
}
