package gridmap

const (
	GridSize = 100
	CellEmpty = 0
	CellShelf = 1
	CellCharging = 2
	CellDockingIn = 3
	CellDockingOut = 4
)

type GridMap struct {
	Width  int
	Height int
	Cells  [][]int
}

type Position struct {
	X int
	Y int
}

func NewGridMap() *GridMap {
	gm := &GridMap{
		Width:  GridSize,
		Height: GridSize,
	}
	gm.Cells = make([][]int, gm.Height)
	for i := range gm.Cells {
		gm.Cells[i] = make([]int, gm.Width)
	}
	gm.generateShelves()
	gm.generateDocking()
	gm.generateCharging()
	return gm
}

func (gm *GridMap) generateShelves() {
	for y := 10; y < 90; y += 6 {
		for x := 10; x < 90; x += 4 {
			for dy := 0; dy < 4; dy++ {
				for dx := 0; dx < 2; dx++ {
					if y+dy < gm.Height && x+dx < gm.Width {
						gm.Cells[y+dy][x+dx] = CellShelf
					}
				}
			}
		}
	}
}

func (gm *GridMap) generateDocking() {
	for x := 5; x < 25; x += 3 {
		gm.Cells[2][x] = CellDockingIn
		gm.Cells[97][x] = CellDockingOut
	}
}

func (gm *GridMap) generateCharging() {
	for i := 0; i < 10; i++ {
		gm.Cells[50][90+i] = CellCharging
	}
}

func (gm *GridMap) IsWalkable(x, y int) bool {
	if x < 0 || x >= gm.Width || y < 0 || y >= gm.Height {
		return false
	}
	return gm.Cells[y][x] == CellEmpty || gm.Cells[y][x] == CellCharging || gm.Cells[y][x] == CellDockingIn || gm.Cells[y][x] == CellDockingOut
}

func (gm *GridMap) GetCell(x, y int) int {
	if x < 0 || x >= gm.Width || y < 0 || y >= gm.Height {
		return -1
	}
	return gm.Cells[y][x]
}

func (gm *GridMap) GetAllShelves() []Position {
	var shelves []Position
	for y := 0; y < gm.Height; y++ {
		for x := 0; x < gm.Width; x++ {
			if gm.Cells[y][x] == CellShelf {
				shelves = append(shelves, Position{X: x, Y: y})
			}
		}
	}
	return shelves
}

func (gm *GridMap) GetDockingInPositions() []Position {
	var pos []Position
	for y := 0; y < gm.Height; y++ {
		for x := 0; x < gm.Width; x++ {
			if gm.Cells[y][x] == CellDockingIn {
				pos = append(pos, Position{X: x, Y: y})
			}
		}
	}
	return pos
}

func (gm *GridMap) GetDockingOutPositions() []Position {
	var pos []Position
	for y := 0; y < gm.Height; y++ {
		for x := 0; x < gm.Width; x++ {
			if gm.Cells[y][x] == CellDockingOut {
				pos = append(pos, Position{X: x, Y: y})
			}
		}
	}
	return pos
}

func (gm *GridMap) GetChargingPositions() []Position {
	var pos []Position
	for y := 0; y < gm.Height; y++ {
		for x := 0; x < gm.Width; x++ {
			if gm.Cells[y][x] == CellCharging {
				pos = append(pos, Position{X: x, Y: y})
			}
		}
	}
	return pos
}

func (gm *GridMap) GetFreeDockingIn() *Position {
	for _, p := range gm.GetDockingInPositions() {
		return &Position{X: p.X, Y: p.Y}
	}
	return nil
}

func (gm *GridMap) GetFreeDockingOut() *Position {
	for _, p := range gm.GetDockingOutPositions() {
		return &Position{X: p.X, Y: p.Y}
	}
	return nil
}
