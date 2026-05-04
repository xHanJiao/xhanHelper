import Combine
import Foundation
import SwiftUI

@MainActor
class AgentViewModel: ObservableObject {
    @Published var agents: [Agent] = []
    @Published var isLoading = false
    @Published var errorMessage: String?
    @Published var showCreateSheet = false
    @Published var editingAgent: Agent?

    private let apiClient: APIClient

    var onChatWithAgent: ((String) -> Void)?

    init(apiClient: APIClient) {
        self.apiClient = apiClient
    }

    func load() async {
        isLoading = true
        do {
            agents = try await apiClient.listAgents()
        } catch {
            errorMessage = error.localizedDescription
        }
        isLoading = false
    }

    func create(name: String, systemPrompt: String) async {
        do {
            let agent = try await apiClient.createAgent(name: name, systemPrompt: systemPrompt)
            agents.insert(agent, at: 0)
            showCreateSheet = false
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    func update(agent: Agent, name: String, systemPrompt: String) async {
        do {
            let updated = try await apiClient.updateAgent(id: agent.id, name: name, systemPrompt: systemPrompt)
            if let idx = agents.firstIndex(where: { $0.id == agent.id }) {
                agents[idx] = updated
            }
            editingAgent = nil
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    func delete(id: String) async {
        do {
            try await apiClient.deleteAgent(id: id)
            agents.removeAll { $0.id == id }
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    func chatWithAgent(_ agent: Agent) {
        onChatWithAgent?(agent.id)
    }
}
