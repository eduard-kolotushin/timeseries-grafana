
import ru.sbrf.devops.Globals
def getConfigPath() {return "$globals.DIR_SRC"}

def onDistrib(app, distr) {
    sh """
    echo "${app.name}"
    """

    withCredentials([string(credentialsId: 'sa-tuz-ci-pvv_bs64', variable: 'TOKEN')]) {
        sh """
            curl -H "Authorization: Basic ${TOKEN}" -o id_commit_distr.txt https://portal.works.prod.sbt/ssd/tools/nexus-cd/repository/sbt_prod/sbt_PROD/CI90000875_view/${app.distrArtifactId}/${app.distrVersion}/${app.distrArtifactId}-${app.distrVersion}-distrib.txt

            # Генерация файла с ID коммита
            git rev-parse HEAD > commit-id.txt

            # Сравниваем файлы
            if cmp -s "id_commit_distr.txt" "commit-id.txt"; then
                echo "id_commit_distr.txt и commit-id.txt совпадают — продолжаем сборку."
            else
                echo "ОШИБКА: id_commit_distr.txt и commit-id.txt НЕ СОВПАДАЮТ!"
                echo "Различия:"
                diff "id_commit_distr.txt" "commit-id.txt"
                exit 1  # Завершаем с ошибкой → джоба падает
            fi
        """
    }
}
return wrapJenkinsfile(this)