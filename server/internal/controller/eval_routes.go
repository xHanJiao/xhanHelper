package controller

import "github.com/gin-gonic/gin"

// EvalRoutesConfig holds dependencies for eval admin routes.
type EvalRoutesConfig struct {
	R               gin.IRouter
	AuthMiddleware  gin.HandlerFunc
	AdminMiddleware gin.HandlerFunc
	Handler         *EvalAdminHandler
}

// RegisterEvalRoutes registers eval admin API routes.
func RegisterEvalRoutes(rc EvalRoutesConfig) {
	admin := rc.R.Group("/api/admin")
	admin.Use(rc.AuthMiddleware, rc.AdminMiddleware)
	{
		eval := admin.Group("/eval")
		{
			eval.GET("/projects", rc.Handler.ListProjects)
			eval.GET("/projects/:project/stats", rc.Handler.GetStats)
			eval.GET("/projects/:project/queries", rc.Handler.ListQueries)
			eval.GET("/projects/:project/queries/:id", rc.Handler.GetQueryDetail)
			eval.PATCH("/projects/:project/queries/:id", rc.Handler.UpdateAnnotations)
			eval.GET("/projects/:project/corpus", rc.Handler.ListCorpus)
			eval.GET("/projects/:project/annotations", rc.Handler.ListAnnotations)
		}
	}
}
