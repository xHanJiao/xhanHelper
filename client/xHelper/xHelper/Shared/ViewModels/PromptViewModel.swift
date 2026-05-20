import Combine
import Foundation
import SwiftUI

@MainActor
class PromptViewModel: ObservableObject {
    @Published var flashPrompts: [UserPrompt] = []
    @Published var proPrompts: [UserPrompt] = []
    @Published var isLoading = false
    @Published var error: String?

    private let apiClient: APIClient

    init(apiClient: APIClient) {
        self.apiClient = apiClient
    }

    func load() async {
        isLoading = true
        do {
            let resp = try await apiClient.listPrompts()
            flashPrompts = resp.prompts.filter { $0.promptType == "flash" }
            proPrompts = resp.prompts.filter { $0.promptType == "pro" }
        } catch {
            self.error = error.localizedDescription
        }
        isLoading = false
    }

    func create(promptType: String, systemPrompt: String) async -> Bool {
        do {
            _ = try await apiClient.createPrompt(promptType: promptType, systemPrompt: systemPrompt)
            await load()
            return true
        } catch {
            self.error = error.localizedDescription
            return false
        }
    }

    func rollback(_ prompt: UserPrompt) async {
        do {
            try await apiClient.rollbackPrompt(id: prompt.id)
            await load()
        } catch {
            self.error = error.localizedDescription
        }
    }

    func delete(_ prompt: UserPrompt) async {
        do {
            try await apiClient.deletePrompt(id: prompt.id)
            await load()
        } catch {
            self.error = error.localizedDescription
        }
    }

    func activePrompt(for type: String) -> UserPrompt? {
        let list = type == "flash" ? flashPrompts : proPrompts
        return list.first { $0.status == "active" }
    }
}
