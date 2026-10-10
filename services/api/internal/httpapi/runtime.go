package httpapi

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

// SetRuntimeShutdown installs process-owned shutdown. Tests and embedded servers
// without a lifecycle callback refuse shutdown instead of pretending to stop.
func (s *Server) SetRuntimeShutdown(stop func()) { s.runtimeStop = stop }

func (s *Server) beginRuntimeOperation() bool {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	if s.runtimeStopping {
		return false
	}
	s.runtimeRequests++
	return true
}
func (s *Server) endRuntimeOperation() { s.runtimeMu.Lock(); s.runtimeRequests--; s.runtimeMu.Unlock() }

func (s *Server) runtimeGate() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path == "/api/v1/health" || c.Request.URL.Path == "/api/v1/runtime/shutdown" {
			c.Next()
			return
		}
		if !s.beginRuntimeOperation() {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"detail": "应用正在关闭，请稍后重试", "code": "runtime_stopping", "retryable": true})
			return
		}
		defer s.endRuntimeOperation()
		c.Next()
	}
}

func (s *Server) runtimeShutdown(c *gin.Context) {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	if s.runtimeStop == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "当前运行环境不支持更新关闭", "code": "runtime_shutdown_unavailable"})
		return
	}
	if s.runtimeStopping {
		c.JSON(http.StatusAccepted, gin.H{"accepted": true})
		return
	}
	if s.runtimeRequests != 0 {
		c.JSON(http.StatusConflict, gin.H{"detail": "有请求或后台调度正在运行，请稍后更新", "code": "runtime_busy", "retryable": true})
		return
	}
	var active int
	err := s.Store.DB.QueryRowContext(c.Request.Context(), `SELECT
 (SELECT COUNT(*) FROM jobs WHERE status IN ('queued','running','cancellation_requested')) +
 (SELECT COUNT(*) FROM architecture_tasks WHERE status IN ('queued','running','cancellation_requested')) +
 (SELECT COUNT(*) FROM retrieval_index_state WHERE status IN ('queued','building'))`).Scan(&active)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "无法检查活动任务", "code": "runtime_preflight_failed"})
		return
	}
	s.retrievalMu.Lock()
	rebuilding := len(s.retrievalRunning) > 0
	s.retrievalMu.Unlock()
	if active > 0 || rebuilding {
		c.JSON(http.StatusConflict, gin.H{"detail": "导入、架构生成或索引任务尚未完成，请稍后更新", "code": "runtime_busy", "retryable": true})
		return
	}
	// HTTP entrypoints and both schedulers share the same admission gate. No
	// dispatcher can create work between the idle check and this state change.
	s.runtimeStopping = true
	c.JSON(http.StatusAccepted, gin.H{"accepted": true})
	c.Writer.Flush()
	go s.runtimeStop()
}
