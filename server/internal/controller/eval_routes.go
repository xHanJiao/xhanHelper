package controller

import "github.com/gin-gonic/gin"

// EvalRoutesConfig 评测标注 API 路由依赖
type EvalRoutesConfig struct {
	R               gin.IRouter
	AuthMiddleware  gin.HandlerFunc
	AdminMiddleware gin.HandlerFunc
	Handler         *EvalAdminHandler
	RunHandler      *EvalRunHandler
}

// RegisterEvalRoutes 注册评测标注 API 路由
func RegisterEvalRoutes(rc EvalRoutesConfig) {
	admin := rc.R.Group("/api/admin")
	admin.Use(rc.AuthMiddleware, rc.AdminMiddleware)
	{
		eval := admin.Group("/eval")
		{
			eval.GET("/projects", rc.Handler.ListProjects)
			eval.GET("/projects/:project/stats", rc.Handler.GetStats)
			eval.GET("/projects/:project/queries", rc.Handler.ListQueries)
			eval.GET("/projects/:project/queries/placed", rc.Handler.ListPlacedQueries)
			eval.GET("/projects/:project/queries/:id", rc.Handler.GetQueryDetail)
			eval.PATCH("/projects/:project/queries/:id", rc.Handler.UpdateAnnotations)
			eval.GET("/projects/:project/corpus", rc.Handler.ListCorpus)
			eval.GET("/projects/:project/annotations", rc.Handler.ListAnnotations)
			eval.GET("/projects/:project/query-annotations", rc.Handler.ListQueryAnnotations)
			eval.POST("/projects/:project/label", rc.Handler.LabelBatch)
			eval.GET("/projects/:project/sessions/:id", rc.Handler.GetSession)

			eval.GET("/projects/:project/runs", rc.RunHandler.ListRuns)
			eval.POST("/projects/:project/runs", rc.RunHandler.CreateRun)
			eval.GET("/projects/:project/runs/:id", rc.RunHandler.GetRun)
			eval.POST("/projects/:project/runs/:id/execute", rc.RunHandler.ExecuteRun)
			eval.DELETE("/projects/:project/runs/:id", rc.RunHandler.DeleteRun)
		}
	}
}
