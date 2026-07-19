import Foundation

actor ClientActionRouter {
    static let shared = ClientActionRouter()

    private var handlers: [String: any ClientActionHandler] = [:]

    func register(_ handler: any ClientActionHandler) {
        handlers[handler.actionType] = handler
    }

    func handle(action: ClientAction) async -> Bool {
        guard let handler = handlers[action.type] else { return false }
        return await handler.handle(action: action)
    }

    func stopAll() async {
        for (_, h) in handlers { await h.stop() }
    }
}
