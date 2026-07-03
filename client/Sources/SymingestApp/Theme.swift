import SwiftUI

public enum Theme {
    public static let bgDark = Color(hex: "0D0C0A")
    public static let bgDarker = Color(hex: "070605")
    public static let bgCard = Color(hex: "12110E").opacity(0.65)
    public static let bgCardHover = Color(hex: "1A1814").opacity(0.8)
    
    public static let goldPrimary = Color(hex: "E5C397")
    public static let goldSecondary = Color(hex: "F8E6CD")
    public static let goldShadow = Color(hex: "C29965")
    
    public static let icePrimary = Color(hex: "EEDCC4")
    public static let iceSecondary = Color(hex: "D4B285")
    public static let iceShadow = Color(hex: "A38054")
    
    public static let textPrimary = Color(hex: "F5F4F0")
    public static let textSecondary = Color(hex: "B5AEA5")
    public static let textMuted = Color(hex: "6E6860")
    
    public static let borderGlass = Color.white.opacity(0.05)
    public static let borderGlassHover = Color(hex: "E5C397").opacity(0.18)
}

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
            (a, r, g, b) = (255, 1, 1, 0)
        }
        self.init(
            .sRGB,
            red: Double(r) / 255,
            green: Double(g) / 255,
            blue: Double(b) / 255,
            opacity: Double(a) / 255
        )
    }
}

// A beautiful background view with glowing blobs and blueprint grid
public struct AmbientBackground: View {
    public init() {}
    public var body: some View {
        ZStack {
            // Base dark background
            Theme.bgDark
                .ignoresSafeArea()
            
            // Blueprint grid matching symaira.com
            BlueprintGrid()
                .opacity(0.15)
                .ignoresSafeArea()
            
            // Glowing blobs
            ZStack {
                // Top-left gold/cyan glow
                RadialGradient(
                    colors: [Theme.goldPrimary.opacity(0.12), .clear],
                    center: .topLeading,
                    startRadius: 0,
                    endRadius: 500
                )
                
                // Bottom-right ice/blue glow
                RadialGradient(
                    colors: [Theme.iceSecondary.opacity(0.08), .clear],
                    center: .bottomTrailing,
                    startRadius: 0,
                    endRadius: 600
                )
            }
            .ignoresSafeArea()
        }
    }
}

public struct BlueprintGrid: View {
    public init() {}
    public var body: some View {
        GeometryReader { geo in
            Path { path in
                let step: CGFloat = 24
                // Vertical lines
                for x in stride(from: 0.0, to: geo.size.width, by: step) {
                    path.move(to: CGPoint(x: x, y: 0))
                    path.addLine(to: CGPoint(x: x, y: geo.size.height))
                }
                // Horizontal lines
                for y in stride(from: 0.0, to: geo.size.height, by: step) {
                    path.move(to: CGPoint(x: 0, y: y))
                    path.addLine(to: CGPoint(x: geo.size.width, y: y))
                }
            }
            .stroke(Color.white.opacity(0.022), lineWidth: 0.75)
        }
    }
}
