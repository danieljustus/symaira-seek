import SwiftUI

extension Color {
    init(hex: String) {
        let hex = hex.trimmingCharacters(in: CharacterSet.alphanumerics.inverted)
        var int: UInt64 = 0
        Scanner(string: hex).scanHexInt64(&int)
        let a, r, g, b: UInt64
        switch hex.count {
        case 3: // RGB (12-bit)
            (a, r, g, b) = (255, (int >> 8) * 17, (int >> 4 & 0xF) * 17, (int & 0xF) * 17)
        case 6: // RGB (24-bit)
            (a, r, g, b) = (255, int >> 16, int >> 8 & 0xFF, int & 0xFF)
        case 8: // ARGB (32-bit)
            (a, r, g, b) = (int >> 24, int >> 16 & 0xFF, int >> 8 & 0xFF, int & 0xFF)
        default:
            (a, r, g, b) = (255, 0, 0, 0)
        }
        self.init(
            .sRGB,
            red: Double(r) / 255,
            green: Double(g) / 255,
            blue:  Double(b) / 255,
            opacity: Double(a) / 255
        )
    }

    // Symaira Theme Colors
    static let symairaBg = Color(hex: "#0D0C0A")
    static let symairaBgDarker = Color(hex: "#070605")
    static let symairaGold = Color(hex: "#E5C397")
    static let symairaGoldSecondary = Color(hex: "#F8E6CD")
    static let symairaGoldShadow = Color(hex: "#C29965")
    static let symairaText = Color(hex: "#F5F4F0")
    static let symairaTextSecondary = Color(hex: "#B5AEA5")
    static let symairaMuted = Color(hex: "#6E6860")
    
    // Transparent Card Backings
    static let symairaCard = Color(hex: "#12110E").opacity(0.65)
    static let symairaCardHover = Color(hex: "#1A1814").opacity(0.8)
    static let symairaBorder = Color(hex: "#FFFFFF").opacity(0.05)
    static let symairaBorderHover = Color(hex: "#E5C397").opacity(0.18)
    
    // Glimmering Accents
    static let symairaGlow = Color(hex: "#E5C397").opacity(0.05)
}

struct SymairaCardModifier: ViewModifier {
    @State private var isHovered = false
    
    func body(content: Content) -> some View {
        content
            .padding()
            .background(isHovered ? Color.symairaCardHover : Color.symairaCard)
            .cornerRadius(12)
            .overlay(
                RoundedRectangle(cornerRadius: 12)
                    .stroke(isHovered ? Color.symairaBorderHover : Color.symairaBorder, lineWidth: 1)
            )
            .animation(.easeInOut(duration: 0.2), value: isHovered)
            .onHover { hover in
                isHovered = hover
            }
    }
}

extension View {
    func symairaCard() -> some View {
        self.modifier(SymairaCardModifier())
    }
}
